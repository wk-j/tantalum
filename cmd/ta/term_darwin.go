package main

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"unicode/utf8"
	"unsafe"
)

func getTermios(fd uintptr) (*syscall.Termios, error) {
	var t syscall.Termios
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TIOCGETA, uintptr(unsafe.Pointer(&t))); errno != 0 {
		return nil, errno
	}
	return &t, nil
}

func setTermios(fd uintptr, t *syscall.Termios) error {
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TIOCSETA, uintptr(unsafe.Pointer(t))); errno != 0 {
		return errno
	}
	return nil
}

func isTerminal(f *os.File) bool {
	_, err := getTermios(f.Fd())
	return err == nil
}

// readHidden reads one line from a terminal with echo off. Canonical mode is
// off too: macOS truncates canonical input lines at 1024 bytes, which would
// silently cut long pasted tokens, so line editing (backspace) is done here.
func readHidden(tty *os.File, prompt string) ([]byte, error) {
	fd := tty.Fd()
	saved, err := getTermios(fd)
	if err != nil {
		return nil, err
	}
	raw := *saved
	raw.Lflag &^= syscall.ECHO | syscall.ICANON
	raw.Lflag |= syscall.ISIG
	raw.Cc[syscall.VMIN] = 1
	raw.Cc[syscall.VTIME] = 0
	if err := setTermios(fd, &raw); err != nil {
		return nil, err
	}

	// Ctrl-C must not leave the user's terminal without echo.
	sigs := make(chan os.Signal, 1)
	done := make(chan struct{})
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		select {
		case s := <-sigs:
			setTermios(fd, saved)
			fmt.Fprintln(os.Stderr)
			os.Exit(128 + int(s.(syscall.Signal)))
		case <-done:
		}
	}()
	defer func() {
		close(done)
		signal.Stop(sigs)
		setTermios(fd, saved)
		fmt.Fprintln(os.Stderr)
	}()

	fmt.Fprint(os.Stderr, prompt)
	var line []byte
	b := make([]byte, 1)
	for {
		n, err := tty.Read(b)
		if err != nil {
			return nil, err
		}
		if n == 0 {
			continue
		}
		switch c := b[0]; c {
		case '\r', '\n':
			return line, nil
		case 0x7f, 0x08: // backspace
			if len(line) > 0 {
				_, size := utf8.DecodeLastRune(line)
				line = line[:len(line)-size]
			}
		case 0x04: // Ctrl-D
			return nil, errors.New("aborted")
		default:
			line = append(line, c)
		}
	}
}
