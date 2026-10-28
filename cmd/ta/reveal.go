package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

// clipboardClearAfter is how long a value copied by `ta reveal -c` stays on
// the clipboard.
const clipboardClearAfter = 45 * time.Second

// clearClipboardCommand is the hidden subcommand that clears the clipboard
// after `ta reveal -c` has exited.
const clearClipboardCommand = "__clear-clipboard"

// confirmUser asks the person at the Mac before a value is shown.
var confirmUser = confirmUserPresence

// cmdReveal shows one value to the person at the Mac, after they approve with
// Touch ID (or an Apple Watch or the login password), on the terminal through
// /dev/tty or, with -c, on the clipboard. It never writes the value to stdout
// or stderr, which agents and logs capture.
func cmdReveal(args []string) error {
	flags := newCommandFlags("reveal")
	toClipboard := flags.Bool("c", false, "")
	if err := flags.parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return errors.New("usage: ta reveal [-e ENV] [-c] NAME")
	}
	env, name := flags.env, flags.Arg(0)
	v, err := openVault()
	if err != nil {
		return err
	}
	envs, err := v.load()
	if err != nil {
		return err
	}
	s, ok := envs[env][name]
	if !ok {
		return fmt.Errorf("%s/%s is %w", env, name, errSecretNotFound)
	}

	// Open the terminal before asking, so a run without one, such as an
	// agent's pipe, fails without putting a prompt in front of the person.
	reason := fmt.Sprintf("copy the secret %s/%s to the clipboard", env, name)
	var tty *os.File
	if !*toClipboard {
		if tty, err = os.OpenFile("/dev/tty", os.O_WRONLY, 0); err != nil {
			return errors.New("no terminal to show the value on; use -c to copy it to the clipboard")
		}
		defer tty.Close()
		reason = fmt.Sprintf("show the secret %s/%s in the terminal", env, name)
	}
	if err := confirmUser(reason); err != nil {
		return fmt.Errorf("not revealed: %v", err)
	}

	value := s.Value
	if tty != nil {
		_, err := fmt.Fprintln(tty, value)
		return err
	}
	count, err := copyToClipboard(value)
	if err != nil {
		return err
	}
	if err := startClipboardClearer(count); err != nil {
		return fmt.Errorf("copied %s/%s, but cannot clear the clipboard later: %v", env, name, err)
	}
	fmt.Fprintf(os.Stderr, "copied %s/%s to the clipboard; it is cleared in %d seconds unless you copy something else first\n",
		env, name, int(clipboardClearAfter/time.Second))
	return nil
}

// startClipboardClearer runs `ta __clear-clipboard COUNT` in its own session,
// so it outlives this command and a closed terminal window.
func startClipboardClearer(changeCount int) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, clearClipboardCommand, strconv.Itoa(changeCount))
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// cmdClearClipboard waits clipboardClearAfter, then empties the clipboard
// unless something else was copied since `ta reveal -c`.
func cmdClearClipboard(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: ta " + clearClipboardCommand + " CHANGE_COUNT")
	}
	count, err := strconv.Atoi(args[0])
	if err != nil {
		return err
	}
	time.Sleep(clipboardClearAfter)
	clearClipboardIfUnchanged(count)
	return nil
}
