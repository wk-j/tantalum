package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"unsafe"
)

// feed carries one of the child's streams into a redactor. The redactor's
// source (a pseudo-terminal master or a pipe's read end) is reachable only
// through the closures, which copy it into that redactor.
type feed struct {
	child  *os.File     // the child's end
	pause  func()       // gives ta's terminal back its own settings
	resume func()       // takes ta's terminal over (raw mode, window size)
	wait   func() error // after the child has exited: waits until its output is through, then pauses
}

// terminal starts a feed for a child that runs on a pseudo-terminal of its
// own because ta runs in the terminal tty: the child then behaves as when run
// directly (colours, progress bars, pagers, prompts, full-screen programs),
// while everything the pseudo-terminal shows, echoed input included, goes
// through r. ta forwards tty's input to it and keeps its window size in step;
// resume puts tty in raw mode so keys such as Ctrl-C reach the child's
// terminal, which turns them into signals as the child's settings say.
//
// The suspend key (Ctrl-Z) is the exception. As the leader of a session of its
// own, the child's process group has no parent in that session, so the kernel
// discards the SIGTSTP its terminal would send to that group. ta therefore
// offers each suspend key that the child's terminal would turn into a signal
// to suspend, with the terminal's foreground process group; suspend returns
// whether it stopped that group itself. Otherwise (a shell inside has another
// job in the foreground, say) the key goes through.
//
// The pseudo-terminal starts with tty's settings, so its output processing
// shows \n as \r\n; newRedactor matches values in that form too.
func (r *redactor) terminal(tty *os.File, suspend func(foreground int) bool) (*feed, error) {
	saved, err := getTermios(tty.Fd())
	if err != nil {
		return nil, err
	}
	master, slave, err := openPTY()
	if err != nil {
		return nil, fmt.Errorf("open a pseudo-terminal: %w", err)
	}
	if err := setTermios(slave.Fd(), saved); err != nil {
		master.Close()
		slave.Close()
		return nil, err
	}
	raw := *saved
	makeRaw(&raw)
	resize := func() {
		var ws winsize
		if ioctl(tty.Fd(), syscall.TIOCGWINSZ, unsafe.Pointer(&ws)) == nil {
			ioctl(master.Fd(), syscall.TIOCSWINSZ, unsafe.Pointer(&ws))
		}
	}
	resize()

	// The kernel tells the child about a new size when the pseudo-terminal
	// gets it; ta hears of it from its own terminal.
	winch := make(chan os.Signal, 1)
	winchDone := make(chan struct{})
	signal.Notify(winch, syscall.SIGWINCH)
	go func() {
		defer close(winchDone)
		for range winch {
			resize()
		}
	}()
	// Keys go to the child's terminal once resume has taken tty over; the copy
	// ends with the first key after the master closes, or with ta.
	var forwarding sync.Once
	forward := func() {
		buf := make([]byte, 4096)
		for {
			n, err := tty.Read(buf)
			if err != nil {
				return
			}
			keys := buf[:n]
			for {
				i := suspendKey(master, keys)
				if i < 0 {
					break
				}
				var group int32
				ioctl(master.Fd(), syscall.TIOCGPGRP, unsafe.Pointer(&group))
				through := keys[:i+1]
				if suspend != nil && suspend(int(group)) {
					through = keys[:i]
				}
				if _, err := master.Write(through); err != nil {
					return
				}
				keys = keys[i+1:]
			}
			if _, err := master.Write(keys); err != nil {
				return
			}
		}
	}
	done := make(chan error, 1)
	go func() {
		_, err := io.Copy(r, master)
		// macOS reports EIO once every descriptor of the slave is closed.
		if errors.Is(err, syscall.EIO) {
			err = nil
		}
		done <- err
	}()

	pause := func() { setTermios(tty.Fd(), saved) }
	return &feed{
		child: slave,
		pause: pause,
		resume: func() {
			setTermios(tty.Fd(), &raw)
			resize()
			forwarding.Do(func() { go forward() })
		},
		wait: func() error {
			// Drop ta's descriptor of the slave, so that the copy ends once the
			// child's (and those of processes it left running) are closed.
			slave.Close()
			err := <-done
			signal.Stop(winch)
			close(winch)
			<-winchDone
			pause()
			master.Close()
			return err
		},
	}, nil
}

// posixVDisable in a control character slot turns that key off (_POSIX_VDISABLE).
const posixVDisable = 0xff

// suspendKey returns the index of the first suspend key in keys if the
// terminal of master turns that key into a signal, else -1.
func suspendKey(master *os.File, keys []byte) int {
	t, err := getTermios(master.Fd())
	if err != nil || t.Lflag&syscall.ISIG == 0 || t.Cc[syscall.VSUSP] == posixVDisable {
		return -1
	}
	return bytes.IndexByte(keys, t.Cc[syscall.VSUSP])
}

// pipe starts a feed for a stream of a child on a pseudo-terminal that does
// not go to ta's terminal (redirected to a file or a pipe): the child writes to
// a pipe, as it would without ta, whose output r redacts. exec.Cmd would make
// the same pipe, but ta waits for a child on a terminal itself (waitStopping).
func (r *redactor) pipe() (*feed, error) {
	pr, pw, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	done := make(chan error, 1)
	go func() {
		_, err := io.Copy(r, pr)
		done <- err
	}()
	return &feed{
		child:  pw,
		pause:  func() {},
		resume: func() {},
		wait: func() error {
			pw.Close()
			err := <-done
			pr.Close()
			return err
		},
	}, nil
}

// waitStopping waits for a child that runs on a terminal of its own. When the
// child stops (Ctrl-Z reaches it through that terminal), ta gives its own
// terminal back and stops too, so the shell's job control works as with the
// child alone; once continued (fg), ta takes the terminal over again and
// continues the child.
func waitStopping(child *os.Process, term *feed) (syscall.WaitStatus, error) {
	for {
		var ws syscall.WaitStatus
		_, err := syscall.Wait4(child.Pid, &ws, syscall.WUNTRACED, nil)
		// WaitStatus.Stopped is false for a stop by SIGSTOP on darwin, which
		// is how ta stops the child's group (see terminal); read the bits.
		stopped := ws&0x7f == 0x7f
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if err != nil || !stopped {
			return ws, err
		}
		term.pause()
		// SIGSTOP rather than SIGTSTP: it stops ta before Kill returns, and
		// even in a process group that the shell no longer controls.
		syscall.Kill(os.Getpid(), syscall.SIGSTOP)
		term.resume()
		syscall.Kill(-child.Pid, syscall.SIGCONT)
	}
}

// sameFile reports whether a and b are the same open file, such as one terminal.
func sameFile(a, b *os.File) bool {
	ai, err := a.Stat()
	if err != nil {
		return false
	}
	bi, err := b.Stat()
	return err == nil && os.SameFile(ai, bi)
}

// makeRaw is cfmakeraw(3): input byte by byte without echo, line editing or
// signal keys, and output without processing.
func makeRaw(t *syscall.Termios) {
	t.Iflag &^= syscall.IGNBRK | syscall.BRKINT | syscall.PARMRK | syscall.ISTRIP | syscall.INLCR | syscall.IGNCR | syscall.ICRNL | syscall.IXON
	t.Oflag &^= syscall.OPOST
	t.Lflag &^= syscall.ECHO | syscall.ECHONL | syscall.ICANON | syscall.ISIG | syscall.IEXTEN
	t.Cflag &^= syscall.CSIZE | syscall.PARENB
	t.Cflag |= syscall.CS8
	t.Cc[syscall.VMIN] = 1
	t.Cc[syscall.VTIME] = 0
}

// winsize is struct winsize of <sys/ttycom.h>.
type winsize struct {
	row, col, xpixel, ypixel uint16
}

func ioctl(fd, request uintptr, arg unsafe.Pointer) error {
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, request, uintptr(arg)); errno != 0 {
		return errno
	}
	return nil
}

// openPTY opens a pseudo-terminal pair (posix_openpt, grantpt, unlockpt,
// ptsname).
func openPTY() (master, slave *os.File, err error) {
	fd, err := syscall.Open("/dev/ptmx", syscall.O_RDWR|syscall.O_NOCTTY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, err
	}
	master = os.NewFile(uintptr(fd), "/dev/ptmx")
	var name [128]byte
	err = ioctl(master.Fd(), syscall.TIOCPTYGRANT, nil)
	if err == nil {
		err = ioctl(master.Fd(), syscall.TIOCPTYUNLK, nil)
	}
	if err == nil {
		err = ioctl(master.Fd(), syscall.TIOCPTYGNAME, unsafe.Pointer(&name[0]))
	}
	n := bytes.IndexByte(name[:], 0)
	if err == nil && n < 0 {
		err = errors.New("pseudo-terminal name is not terminated")
	}
	if err == nil {
		slave, err = os.OpenFile(string(name[:n]), os.O_RDWR|syscall.O_NOCTTY, 0)
	}
	if err != nil {
		master.Close()
		return nil, nil, err
	}
	return master, slave, nil
}
