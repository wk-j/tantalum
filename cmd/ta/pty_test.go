package main

import (
	"bytes"
	"os/exec"
	"strings"
	"syscall"
	"testing"
)

// A child on a terminal feed sees a terminal on stdin, stdout and stderr, reads
// what is typed on ta's terminal, and everything its terminal shows reaches the
// redactor complete, including a multi-line value that the terminal shows with
// \r\n. ta's terminal gets its settings back afterwards.
func TestTerminalFeedRedactsChildOutput(t *testing.T) {
	keyboard, tty, err := openPTY() // tty stands in for ta's terminal
	if err != nil {
		t.Fatal(err)
	}
	defer keyboard.Close()
	defer tty.Close()
	before, err := getTermios(tty.Fd())
	if err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	r := newRedactor(&out, environments{"dev": {"PEM": {Value: "-----BEGIN-----\nabc\n-----END-----"}}})
	term, err := r.terminal(tty, nil)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "-c", `
		[ -t 0 ] && [ -t 1 ] && [ -t 2 ] && echo "all terminals"
		read line; echo "got $line"
		printf 'pem: -----BEGIN-----\nabc\n-----END-----\n'
		head -c 100000 /dev/zero | tr '\0' x
	`)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = term.child, term.child, term.child
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	if err := cmd.Start(); err != nil {
		term.wait()
		t.Fatal(err)
	}
	term.resume()
	if _, err := keyboard.Write([]byte("hello\r")); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	if err := term.wait(); err != nil {
		t.Fatal(err)
	}
	if err := r.Flush(); err != nil {
		t.Fatal(err)
	}

	got := out.String()
	for _, want := range []string{"all terminals\r\n", "got hello\r\n", "pem: [REDACTED:dev/PEM]\r\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("output lacks %q:\n%.300q", want, got)
		}
	}
	if strings.Contains(got, "BEGIN") {
		t.Errorf("value not redacted:\n%.300q", got)
	}
	if n := strings.Count(got, "x"); n != 100000 {
		t.Errorf("got %d of 100000 bytes of the last output", n)
	}
	if after, err := getTermios(tty.Fd()); err != nil || *after != *before {
		t.Errorf("ta's terminal settings after the run differ from before (err %v)", err)
	}
}
