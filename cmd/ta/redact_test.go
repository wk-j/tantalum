package main

import (
	"bytes"
	"testing"
)

func TestRedactorAcrossWriteBoundaries(t *testing.T) {
	envs := environments{
		"dev": {
			"SHORT": {Value: "abc"},
			"LONG":  {Value: "abcdef"},
			"TOKEN": {Value: "tok-123"},
		},
		"prod": {
			"DB_URL": {Value: "pg://prod"},
		},
	}
	cases := []struct {
		name, in, want string
	}{
		{"single secret", "key=tok-123\n", "key=[REDACTED:dev/TOKEN]\n"},
		{"repeated and adjacent", "tok-123tok-123", "[REDACTED:dev/TOKEN][REDACTED:dev/TOKEN]"},
		{"longest match wins", "xabcdefy", "x[REDACTED:dev/LONG]y"},
		{"shorter match when longer fails", "xabcdy", "x[REDACTED:dev/SHORT]dy"},
		{"partial secret at end is not swallowed", "end abcde", "end [REDACTED:dev/SHORT]de"},
		{"prefix only at end is emitted on flush", "tail tok-12", "tail tok-12"},
		{"value from another environment", "url=pg://prod", "url=[REDACTED:prod/DB_URL]"},
		{"no secrets", "plain output", "plain output"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Every two-way split, plus fixed-size chunks down to byte-at-a-time,
			// must produce the same output as a single write.
			var splits [][]string
			for i := 0; i <= len(tc.in); i++ {
				splits = append(splits, []string{tc.in[:i], tc.in[i:]})
			}
			for size := 1; size < len(tc.in); size++ {
				var chunks []string
				for i := 0; i < len(tc.in); i += size {
					chunks = append(chunks, tc.in[i:min(i+size, len(tc.in))])
				}
				splits = append(splits, chunks)
			}
			for _, chunks := range splits {
				var out bytes.Buffer
				r := newRedactor(&out, envs)
				for _, c := range chunks {
					if n, err := r.Write([]byte(c)); err != nil || n != len(c) {
						t.Fatalf("Write(%q) = %d, %v", c, n, err)
					}
				}
				if err := r.Flush(); err != nil {
					t.Fatal(err)
				}
				if got := out.String(); got != tc.want {
					t.Fatalf("chunks %q: got %q, want %q", chunks, got, tc.want)
				}
			}
		})
	}
}
