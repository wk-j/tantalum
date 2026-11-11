package main

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"
	"unicode/utf8"
)

// An export file carries every secret of a vault to another vault or Mac,
// sealed with AES-256-GCM under a key derived from a passphrase the human
// types; it never holds the vault's Keychain key (docs/adr/0006).
const (
	exportFormat  = "tantalum-export"
	exportVersion = 1
	exportKDF     = "pbkdf2-sha256"
	// exportIterations follows OWASP's recommendation for PBKDF2-HMAC-SHA256.
	// Files record their count, so a later ta can raise it and still read
	// these; counts outside [exportIterations, maxExportIterations] are refused.
	exportIterations    = 600_000
	maxExportIterations = 50_000_000
	exportSaltSize      = 16
	// minPassphraseRunes keeps an export file's passphrase out of reach of a
	// quick offline guess; the file may end up anywhere.
	minPassphraseRunes = 12
)

// exportAAD binds a ciphertext to its format and version.
func exportAAD(version int) []byte {
	return fmt.Appendf(nil, "%s v%d", exportFormat, version)
}

// exportEnvelope is the on-disk export file; []byte fields encode as base64.
type exportEnvelope struct {
	Format     string `json:"format"`
	Version    int    `json:"version"`
	KDF        string `json:"kdf"`
	Iterations int    `json:"iterations"`
	Salt       []byte `json:"salt"`
	Nonce      []byte `json:"nonce"`
	Ciphertext []byte `json:"ciphertext"`
}

// errNotExported reports that the person at the Mac refused, cancelled or
// could not answer the prompt.
var errNotExported = errors.New("not exported")

// openTerminal opens the controlling terminal, where passphrases are typed.
// Tests replace it with a pseudo-terminal.
var openTerminal = func() (*os.File, error) {
	return os.OpenFile("/dev/tty", os.O_RDWR, 0)
}

func cmdExport(args []string) error {
	flags := flag.NewFlagSet("export", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 || flags.Arg(0) == "-" {
		return errors.New("usage: ta export FILE   (writes a new encrypted file, never stdout)")
	}
	path := flags.Arg(0)
	// Fail before any prompt when the file exists; the create below is the
	// check that counts.
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("%s already exists; ta export never overwrites a file", path)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	v, err := openVault()
	if err != nil {
		return err
	}
	tty, err := openTerminal()
	if err != nil {
		return errors.New("no terminal to ask for the passphrase on; a human runs ta export in their own terminal")
	}
	defer tty.Close()
	secrets, envCount, err := exportVault(v, path, tty)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "exported %d secrets in %d environments to %s, encrypted with your passphrase\n", secrets, envCount, path)
	return nil
}

// exportVault writes every secret of v to a new file at path, sealed under a
// passphrase read from tty, once the person at the Mac has approved.
func exportVault(v *vault, path string, tty *os.File) (secrets, envCount int, err error) {
	envs, err := v.load()
	if err != nil {
		return 0, 0, err
	}
	for _, s := range envs {
		secrets += len(s)
	}
	if secrets == 0 {
		return 0, 0, errors.New("vault is empty; nothing to export")
	}
	reason := fmt.Sprintf("export all %d secrets of this vault to the encrypted file %s", secrets, filepath.Base(path))
	if err := confirmUser(reason); err != nil {
		return 0, 0, fmt.Errorf("%w: %v", errNotExported, err)
	}
	passphrase, err := readHidden(tty, "passphrase for the export file (input hidden): ")
	if err != nil {
		return 0, 0, err
	}
	if n := utf8.RuneCount(passphrase); n < minPassphraseRunes {
		return 0, 0, fmt.Errorf("passphrase has %d characters; use at least %d. Nothing exported", n, minPassphraseRunes)
	}
	again, err := readHidden(tty, "repeat the passphrase: ")
	if err != nil {
		return 0, 0, err
	}
	if !bytes.Equal(passphrase, again) {
		return 0, 0, errors.New("the passphrases differ; nothing exported")
	}
	data, err := sealExport(envs, passphrase)
	if err != nil {
		return 0, 0, err
	}
	if err := writeNewFile(path, data); err != nil {
		return 0, 0, err
	}
	return secrets, len(envs), nil
}

// exportKey derives the AES-256 key of an export file from its passphrase.
func exportKey(passphrase, salt []byte, iterations int) (cipher.AEAD, error) {
	key, err := pbkdf2.Key(sha256.New, string(passphrase), salt, iterations, keySize)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// sealExport encrypts envs, names and update times included, under passphrase
// with a fresh salt and nonce.
func sealExport(envs environments, passphrase []byte) ([]byte, error) {
	plain, err := json.Marshal(vaultPlaintext{Envs: envs})
	if err != nil {
		return nil, err
	}
	salt := make([]byte, exportSaltSize)
	rand.Read(salt)
	aead, err := exportKey(passphrase, salt, exportIterations)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	rand.Read(nonce)
	return json.Marshal(exportEnvelope{
		Format:     exportFormat,
		Version:    exportVersion,
		KDF:        exportKDF,
		Iterations: exportIterations,
		Salt:       salt,
		Nonce:      nonce,
		Ciphertext: aead.Seal(nil, nonce, plain, exportAAD(exportVersion)),
	})
}

// isExportFile reports whether data claims to be an export file, so import
// asks for its passphrase instead of parsing it as .env.
func isExportFile(data []byte) bool {
	var head struct {
		Format string `json:"format"`
	}
	return json.Unmarshal(data, &head) == nil && head.Format == exportFormat
}

// openExport decrypts an export file and checks every environment, name and
// value in it. Errors name no secret: a name can be sensitive too.
func openExport(data, passphrase []byte) (environments, error) {
	var env exportEnvelope
	if err := json.Unmarshal(data, &env); err != nil || env.Format != exportFormat {
		return nil, errors.New("not a ta export file")
	}
	if env.Version != exportVersion {
		return nil, fmt.Errorf("unsupported export version %d; this ta reads version %d", env.Version, exportVersion)
	}
	if env.KDF != exportKDF || env.Iterations < exportIterations || env.Iterations > maxExportIterations || len(env.Salt) < exportSaltSize {
		return nil, errors.New("export file has unsupported key derivation settings")
	}
	aead, err := exportKey(passphrase, env.Salt, env.Iterations)
	if err != nil {
		return nil, err
	}
	if len(env.Nonce) != aead.NonceSize() {
		return nil, errors.New("export file is corrupted (bad nonce)")
	}
	plain, err := aead.Open(nil, env.Nonce, env.Ciphertext, exportAAD(env.Version))
	if err != nil {
		return nil, errors.New("cannot decrypt the export file: wrong passphrase or corrupted file")
	}
	var payload vaultPlaintext
	if err := json.Unmarshal(plain, &payload); err != nil {
		return nil, fmt.Errorf("decode export file: %w", err)
	}
	if len(payload.Envs) == 0 {
		return nil, errors.New("export file holds no secrets; nothing imported")
	}
	for envName, secrets := range payload.Envs {
		if !validSlug(envName) || len(secrets) == 0 {
			return nil, errors.New("export file holds an invalid or empty environment; nothing imported")
		}
		for name, s := range secrets {
			if !validName(name) || s.Value == "" {
				return nil, errors.New("export file holds an invalid name or an empty value; nothing imported")
			}
		}
	}
	return payload.Envs, nil
}

// importExport opens an export file and merges every environment in it in one
// locked write, keeping existing secrets unless overwrite is set. Imported
// secrets keep the update times they had when exported.
func (v *vault) importExport(data, passphrase []byte, overwrite bool) (imported, skipped, envCount int, err error) {
	incoming, err := openExport(data, passphrase)
	if err != nil {
		return 0, 0, 0, err
	}
	err = v.update(func(envs environments) error {
		now := time.Now().UTC()
		for env, secrets := range incoming {
			if envs[env] == nil {
				envs[env] = map[string]secret{}
			}
			for name, s := range secrets {
				if _, exists := envs[env][name]; exists && !overwrite {
					skipped++
					continue
				}
				if s.Updated.IsZero() {
					s.Updated = now
				}
				envs[env][name] = s
				imported++
			}
		}
		return nil
	})
	return imported, skipped, len(incoming), err
}

// writeNewFile creates path with mode 0600 and writes data, refusing to
// replace anything already there.
func writeNewFile(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("%s already exists; ta export never overwrites a file", path)
	} else if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(path)
	}
	return err
}
