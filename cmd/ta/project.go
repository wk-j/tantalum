package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// vaultLocation is the vault a command uses and why.
type vaultLocation struct {
	dir  string // vault directory; also the Keychain account of its key
	root string // project directory the vault belongs to; "" for the default vault
}

func (l vaultLocation) String() string {
	if l.root == "" {
		return l.dir
	}
	return fmt.Sprintf("%s (project %s)", l.dir, l.root)
}

// tantalumHome holds the default vault and, under projects/, one vault per project.
func tantalumHome() (string, error) {
	if dir := os.Getenv("TANTALUM_HOME"); dir != "" {
		return filepath.Abs(dir)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".tantalum"), nil
}

// locateVault picks the vault for the working directory: inside a git
// repository, the vault keyed by the repository's directory; elsewhere the
// default vault.
func locateVault() (vaultLocation, error) {
	home, err := tantalumHome()
	if err != nil {
		return vaultLocation{}, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return vaultLocation{}, err
	}
	root, ok, err := projectRoot(cwd)
	if err != nil || !ok {
		return vaultLocation{dir: home}, err
	}
	// One spelling per directory, however it was reached (symlink, letter case).
	if root, err = canonicalPath(root); err != nil {
		return vaultLocation{}, err
	}
	return vaultLocation{dir: projectVaultDir(home, root), root: root}, nil
}

// projectVaultDir names a project's vault after its directory: the base name
// for people, plus a hash of the full path so equal base names do not collide.
func projectVaultDir(home, root string) string {
	sum := sha256.Sum256([]byte(root))
	name := []byte(filepath.Base(root))
	for i, c := range name {
		if !(c == '-' || c == '_' || c == '.' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			name[i] = '_'
		}
	}
	return filepath.Join(home, "projects", string(name)+"-"+hex.EncodeToString(sum[:6]))
}

// projectRoot returns the git repository containing dir: the directory that
// holds .git, or for a linked worktree its main checkout, so all worktrees of
// a repository share one vault. ok is false outside any repository.
func projectRoot(dir string) (root string, ok bool, err error) {
	for {
		gitPath := filepath.Join(dir, ".git")
		info, err := os.Stat(gitPath)
		switch {
		case err == nil && info.IsDir():
			return dir, true, nil
		case err == nil:
			root, err := gitFileRoot(gitPath)
			return root, err == nil, err
		case !errors.Is(err, fs.ErrNotExist):
			return "", false, err
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false, nil
		}
		dir = parent
	}
}

// gitFileRoot resolves a .git file ("gitdir: PATH"). A linked worktree's git
// directory has a commondir file leading to the main repository's .git; a
// submodule's has none and is a project of its own.
func gitFileRoot(gitFile string) (string, error) {
	data, err := os.ReadFile(gitFile)
	if err != nil {
		return "", err
	}
	gitDir, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir: ")
	if !ok {
		return "", fmt.Errorf("%s: expected \"gitdir: PATH\"", gitFile)
	}
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(filepath.Dir(gitFile), gitDir)
	}
	common, err := os.ReadFile(filepath.Join(gitDir, "commondir"))
	if errors.Is(err, fs.ErrNotExist) {
		return filepath.Dir(gitFile), nil
	} else if err != nil {
		return "", err
	}
	commonDir := strings.TrimSpace(string(common))
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(gitDir, commonDir)
	}
	return filepath.Dir(filepath.Clean(commonDir)), nil
}
