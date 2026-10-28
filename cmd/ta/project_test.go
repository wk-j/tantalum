package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t", "-c", "init.defaultBranch=main"}, args...)...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func vaultFor(t *testing.T, cwd string) vaultLocation {
	t.Helper()
	t.Chdir(cwd)
	loc, err := locateVault()
	if err != nil {
		t.Fatalf("%s: %v", cwd, err)
	}
	return loc
}

func TestVaultIsKeyedByRepositoryDirectory(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	home := t.TempDir()
	t.Setenv("TANTALUM_HOME", home)
	base := t.TempDir()
	api := filepath.Join(base, "api")
	sameName := filepath.Join(base, "other", "api")
	for _, dir := range []string{filepath.Join(api, "src"), sameName} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	git(t, api, "init", "-q")
	git(t, api, "commit", "-q", "--allow-empty", "-m", "init")
	git(t, api, "worktree", "add", "-q", filepath.Join(base, "api-wt"))
	git(t, sameName, "init", "-q")

	root := vaultFor(t, api)
	if root.root == "" || filepath.Dir(root.dir) != filepath.Join(home, "projects") {
		t.Fatalf("repo root: got %+v, want a vault under %s/projects", root, home)
	}
	if got := vaultFor(t, filepath.Join(api, "src")); got != root {
		t.Errorf("subdirectory: got %+v, want %+v", got, root)
	}
	if got := vaultFor(t, filepath.Join(base, "api-wt")); got != root {
		t.Errorf("worktree: got %+v, want the main checkout's %+v", got, root)
	}
	if got := vaultFor(t, sameName); got.dir == root.dir {
		t.Errorf("another repo also named api shares vault %s", got.dir)
	}
	if got := vaultFor(t, t.TempDir()); got.root != "" || got.dir != home {
		t.Errorf("outside any repo: got %+v, want the default vault %s", got, home)
	}
}

// The same directory reached through a symlink or with different letter case
// must not open a second, empty vault.
func TestVaultIgnoresSymlinksAndLetterCase(t *testing.T) {
	t.Setenv("TANTALUM_HOME", t.TempDir())
	base := t.TempDir()
	repo := filepath.Join(base, "Repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(repo, link); err != nil {
		t.Fatal(err)
	}
	want := vaultFor(t, repo)
	for _, cwd := range []string{link, filepath.Join(base, "repo")} {
		if got := vaultFor(t, cwd); got != want {
			t.Errorf("%s: got %+v, want %+v", cwd, got, want)
		}
	}
}
