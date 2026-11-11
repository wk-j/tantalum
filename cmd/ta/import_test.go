package main

import (
	"bytes"
	"errors"
	"flag"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestParseDotenv(t *testing.T) {
	cases := []struct {
		name, input string
		want        map[string]string
	}{
		{"assignments", "# comment\n\nAPI_KEY=value\n export DB_URL = postgres://localhost/db?a=b \n_lower_2=yes\n", map[string]string{
			"API_KEY": "value", "DB_URL": "postgres://localhost/db?a=b", "_lower_2": "yes",
		}},
		{"comments", "A=value # comment\nB=value\t# comment\nC=value#literal\nD=\"# quoted\" # comment\nE='literal # value'\n", map[string]string{
			"A": "value", "B": "value", "C": "value#literal", "D": "# quoted", "E": "literal # value",
		}},
		{"quotes and escapes", `A="line\nreturn\rtab\tquote\"slash\\unknown\q"
B='literal\n\t\\'
C="  padded  "
`, map[string]string{
			"A": "line\nreturn\rtab\tquote\"slash\\unknown\\q", "B": `literal\n\t\\`, "C": "  padded  ",
		}},
		{"quote escape parity", `A="ends with slash\\"
B="escaped quote\"and slash\\"
C='single quotes keep a slash\'
`, map[string]string{"A": `ends with slash\`, "B": `escaped quote"and slash\`, "C": `single quotes keep a slash\`}},
		{"multiline", "A=\"first  \n  second\nthird\"\nB='one\ntwo'\nC=end", map[string]string{
			"A": "first  \n  second\nthird", "B": "one\ntwo", "C": "end",
		}},
		{"line endings and BOM", "\ufeffA=one\r\nB='two\rthree'\rC=four", map[string]string{
			"A": "one", "B": "two\nthree", "C": "four",
		}},
		{"duplicate names", "A=first\nB=other\nA=last", map[string]string{"A": "last", "B": "other"}},
		{"literal shell syntax", "A=${HOME}\nB=$(touch sentinel)\nC=`id`\nD=$HOME", map[string]string{
			"A": "${HOME}", "B": "$(touch sentinel)", "C": "`id`", "D": "$HOME",
		}},
		{"large value", "A=" + strings.Repeat("x", 128<<10), map[string]string{"A": strings.Repeat("x", 128<<10)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseDotenv([]byte(tc.input))
			if err != nil {
				t.Fatal(err)
			}
			if !maps.Equal(got, tc.want) {
				t.Fatalf("parsed %q, want %q", got, tc.want)
			}
		})
	}
}

func TestImportDotenvRejectsInvalidInputWithoutChangingTheVault(t *testing.T) {
	cases := []struct {
		name, input, wantError string
	}{
		{"empty file", "", "no secrets"},
		{"comments only", "\n# comment\n", "no secrets"},
		{"missing equals", "GOOD=value\nprivate-value", "line 2: expected NAME=VALUE"},
		{"empty name", "GOOD=value\n=private-value", "line 2: expected NAME=VALUE"},
		{"invalid name", "GOOD=value\nprivate-value=private-value", "line 2: invalid secret name"},
		{"numeric name", "GOOD=value\n9BAD=private-value", "line 2: invalid secret name"},
		{"empty value", "GOOD=value\nEMPTY=", "line 2: empty value"},
		{"comment only value", "GOOD=value\nEMPTY= # private-value", "line 2: empty value"},
		{"empty single quotes", "GOOD=value\nEMPTY=''", "line 2: empty value"},
		{"empty double quotes", "GOOD=value\nEMPTY=\"\"", "line 2: empty value"},
		{"unterminated quotes", "GOOD=value\nBAD=\"private-value\nmore", "line 2: missing closing quote"},
		{"trailing garbage", "GOOD=value\nBAD='private-value'private-value", "line 2: unexpected text after closing quote"},
		{"bad duplicate", "GOOD=value\nGOOD=", "line 2: empty value"},
		{"earlier bad duplicate", "BAD=\nBAD=private-value", "line 1: empty value"},
		{"line after multiline", "GOOD='one\ntwo'\nprivate-value", "line 3: expected NAME=VALUE"},
		{"invalid UTF-8", "GOOD=value\nBAD=private-value\xff", "input must be UTF-8"},
		{"NUL", "GOOD=value\nBAD=private-value\x00", "input must not contain NUL"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := newTestVault(t)
			if err := v.set("dev", "KEEP", "original-value"); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(filepath.Join(v.dir, vaultFile))
			if err != nil {
				t.Fatal(err)
			}
			for _, overwrite := range []bool{false, true} {
				_, _, err := v.importDotenv("dev", []byte(tc.input), overwrite)
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("error = %v, want %q", err, tc.wantError)
				}
				if strings.Contains(err.Error(), "private-value") {
					t.Fatalf("error exposes input: %v", err)
				}
				after, err := os.ReadFile(filepath.Join(v.dir, vaultFile))
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(before, after) {
					t.Fatal("invalid input changed the vault file")
				}
			}
		})
	}
}

func TestImportDotenvKeepsExistingUnlessOverwriteIsSet(t *testing.T) {
	for _, overwrite := range []bool{false, true} {
		t.Run(map[bool]string{false: "keep", true: "overwrite"}[overwrite], func(t *testing.T) {
			v := newTestVault(t)
			for _, env := range []string{"dev", "prod"} {
				if err := v.set(env, "EXISTING", "original-value"); err != nil {
					t.Fatal(err)
				}
			}
			before, err := v.load()
			if err != nil {
				t.Fatal(err)
			}
			imported, skipped, err := v.importDotenv("dev", []byte("EXISTING=replacement\nNEW=first\nNEW=last"), overwrite)
			if err != nil {
				t.Fatal(err)
			}
			wantImported, wantSkipped := 1, 1
			if overwrite {
				wantImported, wantSkipped = 2, 0
			}
			if imported != wantImported || skipped != wantSkipped {
				t.Fatalf("imported %d, skipped %d; want %d, %d", imported, skipped, wantImported, wantSkipped)
			}
			after, err := v.load()
			if err != nil {
				t.Fatal(err)
			}
			if got := after["dev"]["NEW"]; got.Value != "last" || got.Updated.IsZero() {
				t.Fatalf("new secret = %+v", got)
			}
			if overwrite {
				if got := after["dev"]["EXISTING"]; got.Value != "replacement" || !got.Updated.Equal(after["dev"]["NEW"].Updated) {
					t.Fatalf("replacement = %+v", got)
				}
			} else if after["dev"]["EXISTING"] != before["dev"]["EXISTING"] {
				t.Fatal("skipped secret or its timestamp changed")
			}
			if after["prod"]["EXISTING"] != before["prod"]["EXISTING"] || len(after["prod"]) != 1 {
				t.Fatal("import changed a different environment")
			}
			raw, err := os.ReadFile(filepath.Join(v.dir, vaultFile))
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(raw, []byte("replacement")) || bytes.Contains(raw, []byte("original-value")) {
				t.Fatal("vault contains plaintext import values")
			}
		})
	}
}

func TestImportDotenvInvalidInputDoesNotCreateEnvironmentOrLock(t *testing.T) {
	v := newTestVault(t)
	if _, _, err := v.importDotenv("new-env", []byte("A=valid\nB="), false); err == nil {
		t.Fatal("invalid import succeeded")
	}
	if _, err := os.Stat(filepath.Join(v.dir, lockFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid import created a lock: %v", err)
	}
	envs, err := v.load()
	if err != nil || len(envs) != 0 {
		t.Fatalf("invalid import changed an empty vault: %v, %v", envs, err)
	}
}

func TestImportDotenvValuesAreRedacted(t *testing.T) {
	v := newTestVault(t)
	if _, _, err := v.importDotenv(defaultEnv, []byte("API_KEY='imported-private-value'"), false); err != nil {
		t.Fatal(err)
	}
	envs, err := v.load()
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	r := newRedactor(&output, envs)
	if _, err := r.Write([]byte("key: imported-private-value")); err != nil {
		t.Fatal(err)
	}
	if err := r.Flush(); err != nil {
		t.Fatal(err)
	}
	if got := output.String(); got != "key: [REDACTED:default/API_KEY]" {
		t.Fatalf("redacted output = %q", got)
	}
}

func TestImportDotenvConcurrentImportsKeepTheFirstValue(t *testing.T) {
	v := newTestVault(t)
	var wg sync.WaitGroup
	results := make(chan int, 8)
	for i := range 8 {
		wg.Go(func() {
			imported, _, err := v.importDotenv(defaultEnv, []byte("KEY=value-"+string(rune('a'+i))), false)
			if err != nil {
				t.Error(err)
			}
			results <- imported
		})
	}
	wg.Wait()
	close(results)
	total := 0
	for n := range results {
		total += n
	}
	if total != 1 {
		t.Fatalf("%d imports wrote the same secret without overwrite, want 1", total)
	}
}

func TestReadImportFileAndStdin(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input.env")
	const input = "A=file-value\nB='multi\nline'\n"
	if err := os.WriteFile(path, []byte(input), 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := readImportFile(path)
	if err != nil || string(data) != input {
		t.Fatalf("file input = %q, %v", data, err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	old := os.Stdin
	os.Stdin = f
	t.Cleanup(func() { os.Stdin = old })
	data, err = readImportFile("-")
	if err != nil || string(data) != input {
		t.Fatalf("stdin input = %q, %v", data, err)
	}
	if _, err := readImportFile(path + ".missing"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing file = %v, want not found", err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := readImportFile("-"); err == nil {
		t.Fatal("closed stdin read succeeded")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != input {
		t.Fatalf("source file changed: %q, %v", after, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("source file permissions changed: %v, %v", info, err)
	}
}

func TestCmdImportRefusesInteractiveStdin(t *testing.T) {
	keyboard, tty, err := openPTY()
	if err != nil {
		t.Fatal(err)
	}
	defer keyboard.Close()
	defer tty.Close()
	old := os.Stdin
	os.Stdin = tty
	t.Cleanup(func() { os.Stdin = old })
	if err := cmdImport([]string{"-"}); err == nil || !strings.Contains(err.Error(), "refusing to read .env values from a terminal") {
		t.Fatalf("interactive import = %v, want refusal before reading input", err)
	}
}

func TestCmdImportValidatesArgumentsBeforeOpeningTheVault(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{nil, "usage: ta import"},
		{[]string{"one", "two"}, "usage: ta import"},
		{[]string{"-e", "bad/env", "input.env"}, "invalid environment"},
		{[]string{"-unknown", "input.env"}, "flag provided but not defined"},
	} {
		if err := cmdImport(tc.args); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("import %v: error = %v, want %q", tc.args, err, tc.want)
		}
	}
	if err := cmdImport([]string{"-h"}); !errors.Is(err, flag.ErrHelp) {
		t.Errorf("import -h = %v, want flag.ErrHelp", err)
	}
	t.Setenv("TANTALUM_HOME", t.TempDir())
	t.Chdir(t.TempDir())
	input, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	old := os.Stdin
	os.Stdin = input
	t.Cleanup(func() { os.Stdin = old })
	if err := cmdImport([]string{"-e", "prod", "-overwrite", "-"}); err == nil || !strings.Contains(err.Error(), "ta init") {
		t.Fatalf("import without vault = %v, want initialization error before reading stdin", err)
	}
}
