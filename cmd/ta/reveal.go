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

// clipboardClearAfter is how long a revealed value stays on the clipboard.
const clipboardClearAfter = 45 * time.Second

// clearClipboardCommand is the hidden subcommand that clears the clipboard
// after the command or request that copied a value has finished.
const clearClipboardCommand = "__clear-clipboard"

// errNotRevealed reports that the person at the Mac refused, cancelled or
// could not answer the prompt.
var errNotRevealed = errors.New("not revealed")

// confirmUser asks the person at the Mac before a value is shown.
var confirmUser = confirmUserPresence

// clearClipboardLater empties the clipboard clipboardClearAfter from now
// unless something else is copied first. Tests replace it so they start no
// clearer process.
var clearClipboardLater = startClipboardClearer

// cmdReveal shows one value to the person at the Mac through revealSecret.
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
	if err := revealSecret(v, env, name, *toClipboard); err != nil {
		return err
	}
	if *toClipboard {
		fmt.Fprintf(os.Stderr, "copied %s/%s to the clipboard; it is cleared in %d seconds unless you copy something else first\n",
			env, name, int(clipboardClearAfter/time.Second))
	}
	return nil
}

// revealSecret shows the value of env/name to the person at the Mac, after
// they approve with Touch ID (or an Apple Watch or the login password): on the
// terminal through /dev/tty or, with toClipboard, on the clipboard until
// clipboardClearAfter. `ta reveal` and the web UI's reveal both go through it.
// It never writes the value to stdout, stderr or an HTTP response, which
// agents and logs capture.
func revealSecret(v *vault, env, name string, toClipboard bool) error {
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
	if !toClipboard {
		if tty, err = os.OpenFile("/dev/tty", os.O_WRONLY, 0); err != nil {
			return errors.New("no terminal to show the value on; use -c to copy it to the clipboard")
		}
		defer tty.Close()
		reason = fmt.Sprintf("show the secret %s/%s in the terminal", env, name)
	}
	if err := confirmUser(reason); err != nil {
		return fmt.Errorf("%w: %v", errNotRevealed, err)
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
	if err := clearClipboardLater(count); err != nil {
		return fmt.Errorf("copied %s/%s, but cannot clear the clipboard later: %v", env, name, err)
	}
	return nil
}

// startClipboardClearer runs `ta __clear-clipboard COUNT` in its own session,
// so it outlives this command, a closed terminal window and a stopped
// `ta serve`.
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
	go cmd.Wait() // reaps it while `ta serve` keeps running
	return nil
}

// cmdClearClipboard waits clipboardClearAfter, then empties the clipboard
// unless something else was copied since the reveal.
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
