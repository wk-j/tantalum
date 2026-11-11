package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestExportVaultRoundTrip(t *testing.T) {
	v := newTestVault(t)
	for _, s := range [][3]string{{"dev", "API_KEY", "dev-value"}, {"prod", "API_KEY", "prod-value"}, {"prod", "DB_URL", "postgres://x"}} {
		if err := v.set(s[0], s[1], s[2]); err != nil {
			t.Fatal(err)
		}
	}
	want, err := v.load()
	if err != nil {
		t.Fatal(err)
	}
	var reasons []string
	railsConfirm(t, func(reason string) error { reasons = append(reasons, reason); return nil })
	path := filepath.Join(t.TempDir(), "secrets.ta-export")
	secrets, envCount, err := exportVault(v, path, railsTerminal(t, railsPassphrase, railsPassphrase))
	if err != nil {
		t.Fatal(err)
	}
	if secrets != 3 || envCount != 2 {
		t.Errorf("exported %d secrets in %d environments, want 3 in 2", secrets, envCount)
	}
	if len(reasons) != 1 || !strings.Contains(reasons[0], "3 secrets") || !strings.Contains(reasons[0], "secrets.ta-export") {
		t.Errorf("prompt reasons %q; want one that names the count and the file", reasons)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !isExportFile(data) {
		t.Fatal("isExportFile does not recognise an export file")
	}

	other := newTestVault(t)
	imported, skipped, envs, err := other.importExport(data, []byte(railsPassphrase), false)
	if err != nil {
		t.Fatal(err)
	}
	if imported != 3 || skipped != 0 || envs != 2 {
		t.Errorf("imported %d, kept %d, %d environments; want 3, 0, 2", imported, skipped, envs)
	}
	got, err := other.load()
	if err != nil {
		t.Fatal(err)
	}
	for env, secrets := range want {
		for name, s := range secrets {
			if g := got[env][name]; g.Value != s.Value || !g.Updated.Equal(s.Updated) {
				t.Errorf("%s/%s = %q updated %v, want %q updated %v", env, name, g.Value, g.Updated, s.Value, s.Updated)
			}
		}
	}
}

func TestExportVaultRefusesWeakOrMistypedPassphrases(t *testing.T) {
	v := newTestVault(t)
	if err := v.set("dev", "A", "value"); err != nil {
		t.Fatal(err)
	}
	railsConfirm(t, func(string) error { return nil })
	for _, tc := range []struct {
		name  string
		lines []string
		want  string
	}{
		{"short", []string{"elevenchars"}, "at least 12"},
		{"short multibyte", []string{"รหัสผ่าน"}, "at least 12"}, // 8 code points, 24 bytes
		{"empty", []string{""}, "at least 12"},
		{"mistyped", []string{railsPassphrase, railsPassphrase + "x"}, "differ"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "out.ta-export")
			if _, _, err := exportVault(v, path, railsTerminal(t, tc.lines...)); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("a refused passphrase left a file: %v", err)
			}
		})
	}
	// Code points, not bytes: 12 Thai letters (36 bytes) pass.
	thai := "ภาษาไทยยาวพอ"
	path := filepath.Join(t.TempDir(), "thai.ta-export")
	if _, _, err := exportVault(v, path, railsTerminal(t, thai, thai)); err != nil {
		t.Fatalf("12-character passphrase refused: %v", err)
	}
}

func TestExportVaultOfAnEmptyVaultAsksNothing(t *testing.T) {
	asked := false
	railsConfirm(t, func(string) error { asked = true; return nil })
	_, _, err := exportVault(newTestVault(t), filepath.Join(t.TempDir(), "out"), nil)
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("error = %v, want an empty-vault error", err)
	}
	if asked {
		t.Error("an empty vault raised the prompt")
	}
}

func TestImportExportKeepsExistingUnlessOverwriteIsSet(t *testing.T) {
	exported := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	data, err := sealExport(environments{
		"dev":  {"EXISTING": {Value: "exported-value", Updated: exported}, "NEW": {Value: "new-value", Updated: exported}},
		"prod": {"EXISTING": {Value: "exported-prod", Updated: exported}},
	}, []byte(railsPassphrase))
	if err != nil {
		t.Fatal(err)
	}
	for _, overwrite := range []bool{false, true} {
		v := newTestVault(t)
		if err := v.set("dev", "EXISTING", "local-value"); err != nil {
			t.Fatal(err)
		}
		imported, skipped, _, err := v.importExport(data, []byte(railsPassphrase), overwrite)
		if err != nil {
			t.Fatal(err)
		}
		envs, err := v.load()
		if err != nil {
			t.Fatal(err)
		}
		wantExisting, wantImported, wantSkipped := "local-value", 2, 1
		if overwrite {
			wantExisting, wantImported, wantSkipped = "exported-value", 3, 0
		}
		if imported != wantImported || skipped != wantSkipped {
			t.Errorf("overwrite=%v: imported %d, kept %d; want %d, %d", overwrite, imported, skipped, wantImported, wantSkipped)
		}
		if got := envs["dev"]["EXISTING"].Value; got != wantExisting {
			t.Errorf("overwrite=%v: dev/EXISTING = %q, want %q", overwrite, got, wantExisting)
		}
		if s := envs["dev"]["NEW"]; s.Value != "new-value" || !s.Updated.Equal(exported) {
			t.Errorf("overwrite=%v: dev/NEW = %q updated %v, want new-value updated %v", overwrite, s.Value, s.Updated, exported)
		}
		if envs["prod"]["EXISTING"].Value != "exported-prod" {
			t.Errorf("overwrite=%v: prod/EXISTING not imported", overwrite)
		}
	}
}

func TestIsExportFileLeavesDotenvAlone(t *testing.T) {
	for _, input := range []string{"A=value\n", `{"format":"other"}`, `{"A":"value"}`, "", "{"} {
		if isExportFile([]byte(input)) {
			t.Errorf("isExportFile(%q) = true", input)
		}
	}
}

func TestCmdExportValidatesArgumentsBeforeOpeningTheVault(t *testing.T) {
	for _, args := range [][]string{nil, {"a", "b"}, {"-"}, {"-e", "dev", "out"}} {
		if err := cmdExport(args); err == nil || strings.Contains(err.Error(), "vault") {
			t.Errorf("export %v: error = %v, want a usage or flag error before the vault is opened", args, err)
		}
	}
	t.Setenv("TANTALUM_HOME", t.TempDir())
	t.Chdir(t.TempDir())
	if err := cmdExport([]string{"out.ta-export"}); err == nil || !strings.Contains(err.Error(), "ta init") {
		t.Fatalf("export without vault = %v, want initialization error", err)
	}
}
