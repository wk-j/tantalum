package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

const (
	vaultFile = "vault.json"
	lockFile  = "vault.lock"
	keySize   = 32 // AES-256
	// vaultVersion 2 groups secrets into environments. Version 1 held one flat
	// set of secrets; it still loads, as environment "default", and is
	// rewritten as version 2 on the next change.
	vaultVersion = 2
)

// vaultAAD binds a ciphertext to its format version.
func vaultAAD(version int) []byte {
	return fmt.Appendf(nil, "tantalum vault v%d", version)
}

type secret struct {
	Value   string    `json:"value"`
	Updated time.Time `json:"updated"`
}

// environments maps environment name → secret name → secret. An environment
// exists only while it holds at least one secret.
type environments map[string]map[string]secret

// vaultPlaintext is what gets encrypted: names are hidden on disk too.
type vaultPlaintext struct {
	Envs environments `json:"envs"`
}

// vaultPlaintextV1 is the version 1 layout.
type vaultPlaintextV1 struct {
	Secrets map[string]secret `json:"secrets"`
}

// envelope is the on-disk file; []byte fields encode as base64.
type envelope struct {
	Version    int    `json:"version"`
	Nonce      []byte `json:"nonce"`
	Ciphertext []byte `json:"ciphertext"`
}

type vault struct {
	vaultLocation
	aead cipher.AEAD
}

func newVault(loc vaultLocation, key []byte) (*vault, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &vault{vaultLocation: loc, aead: aead}, nil
}

// openVault locates the vault for the working directory and fetches its key
// from the Keychain.
func openVault() (*vault, error) {
	loc, err := locateVault()
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(loc.dir, vaultFile)); errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("no vault in %s; a human runs `ta init` here first", loc)
	} else if err != nil {
		return nil, err
	}
	key, err := keychainGet(loc.dir)
	if errors.Is(err, errNoKey) {
		return nil, fmt.Errorf("vault in %s has no key in the login Keychain (service %q)", loc, keychainService)
	} else if err != nil {
		return nil, err
	}
	return newVault(loc, key)
}

func (v *vault) load() (environments, error) {
	raw, err := os.ReadFile(filepath.Join(v.dir, vaultFile))
	if err != nil {
		return nil, err
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("read vault: %w", err)
	}
	if env.Version != 1 && env.Version != vaultVersion {
		return nil, fmt.Errorf("unsupported vault version %d; this ta reads versions 1 and %d", env.Version, vaultVersion)
	}
	if len(env.Nonce) != v.aead.NonceSize() {
		return nil, errors.New("vault file is corrupted (bad nonce)")
	}
	plain, err := v.aead.Open(nil, env.Nonce, env.Ciphertext, vaultAAD(env.Version))
	if err != nil {
		return nil, errors.New("cannot decrypt vault: wrong Keychain key or corrupted file")
	}
	envs := environments{}
	if env.Version == 1 {
		var data vaultPlaintextV1
		if err := json.Unmarshal(plain, &data); err != nil {
			return nil, fmt.Errorf("decode vault: %w", err)
		}
		if len(data.Secrets) > 0 {
			envs[defaultEnv] = data.Secrets
		}
		return envs, nil
	}
	var data vaultPlaintext
	if err := json.Unmarshal(plain, &data); err != nil {
		return nil, fmt.Errorf("decode vault: %w", err)
	}
	if data.Envs != nil {
		envs = data.Envs
	}
	return envs, nil
}

func (v *vault) save(envs environments) error {
	plain, err := json.Marshal(vaultPlaintext{Envs: envs})
	if err != nil {
		return err
	}
	nonce := make([]byte, v.aead.NonceSize())
	rand.Read(nonce)
	raw, err := json.Marshal(envelope{
		Version:    vaultVersion,
		Nonce:      nonce,
		Ciphertext: v.aead.Seal(nil, nonce, plain, vaultAAD(vaultVersion)),
	})
	if err != nil {
		return err
	}
	return writeFileAtomic(v.dir, vaultFile, raw)
}

// update runs a read-modify-write under an exclusive lock, so concurrent
// changes (several agents, terminals, the web UI) cannot drop each other's.
func (v *vault) update(fn func(environments) error) error {
	lock, err := os.OpenFile(filepath.Join(v.dir, lockFile), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	envs, err := v.load()
	if err != nil {
		return err
	}
	if err := fn(envs); err != nil {
		return err
	}
	return v.save(envs)
}

var errSecretNotFound = errors.New("not in the vault")

func (v *vault) set(env, name, value string) error {
	return v.update(func(envs environments) error {
		if envs[env] == nil {
			envs[env] = map[string]secret{}
		}
		envs[env][name] = secret{Value: value, Updated: time.Now().UTC()}
		return nil
	})
}

func (v *vault) remove(env, name string) error {
	return v.update(func(envs environments) error {
		if _, ok := envs[env][name]; !ok {
			return fmt.Errorf("%s/%s is %w", env, name, errSecretNotFound)
		}
		delete(envs[env], name)
		if len(envs[env]) == 0 {
			delete(envs, env)
		}
		return nil
	})
}

// writeFileAtomic replaces dir/name via a synced temp file and rename, so
// readers see either the old or the new vault, never a partial one.
func writeFileAtomic(dir, name string, data []byte) error {
	tmp, err := os.CreateTemp(dir, "."+name+".*") // created with mode 0600
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), filepath.Join(dir, name)); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
