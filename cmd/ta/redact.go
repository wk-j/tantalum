package main

import (
	"bytes"
	"cmp"
	"io"
	"slices"
	"strings"
)

// redactor is an io.Writer that replaces every secret value with
// [REDACTED:ENV/NAME] before writing to w. A secret split across two writes is
// still caught: bytes that could begin an incomplete match are held until the
// next write or Flush, which must be called once the stream ends.
type redactor struct {
	w       io.Writer
	secrets []redaction // longest value first, so the longest match wins
	first   [256]bool   // first bytes of all values, to skip most positions fast
	pending []byte
}

type redaction struct {
	value []byte
	label []byte
}

// newRedactor redacts the values of every environment, labelled ENV/NAME. A
// value with line breaks is also matched with \r\n for \n, as a terminal
// shows it (output processing of a pseudo-terminal, see redactor.terminal).
func newRedactor(w io.Writer, envs environments) *redactor {
	r := &redactor{w: w}
	for env, secrets := range envs {
		for name, s := range secrets {
			if s.Value == "" { // would match everywhere without consuming input
				continue
			}
			label := []byte("[REDACTED:" + env + "/" + name + "]")
			r.secrets = append(r.secrets, redaction{value: []byte(s.Value), label: label})
			if crlf := strings.ReplaceAll(s.Value, "\n", "\r\n"); crlf != s.Value {
				r.secrets = append(r.secrets, redaction{value: []byte(crlf), label: label})
			}
			r.first[s.Value[0]] = true
		}
	}
	slices.SortFunc(r.secrets, func(a, b redaction) int {
		return cmp.Or(cmp.Compare(len(b.value), len(a.value)), bytes.Compare(a.label, b.label))
	})
	return r
}

func (r *redactor) Write(p []byte) (int, error) {
	r.pending = append(r.pending, p...)
	if err := r.drain(false); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Flush writes held-back bytes; they cannot complete a secret any more.
func (r *redactor) Flush() error {
	return r.drain(true)
}

func (r *redactor) drain(final bool) error {
	buf := r.pending
	out := make([]byte, 0, len(buf))
	i := 0
scan:
	for i < len(buf) {
		if r.first[buf[i]] {
			rest := buf[i:]
			for _, s := range r.secrets {
				if bytes.HasPrefix(rest, s.value) {
					out = append(out, s.label...)
					i += len(s.value)
					continue scan
				}
				if !final && len(rest) < len(s.value) && bytes.HasPrefix(s.value, rest) {
					break scan // the rest of this secret may arrive in the next write
				}
			}
		}
		out = append(out, buf[i])
		i++
	}
	r.pending = append(r.pending[:0], buf[i:]...)
	if len(out) == 0 {
		return nil
	}
	_, err := r.w.Write(out)
	return err
}
