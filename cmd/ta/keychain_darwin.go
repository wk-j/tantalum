package main

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// The vault key is a generic password in the login Keychain: service
// "tantalum", account = absolute vault directory (one key per vault).
const (
	keychainService = "tantalum"
	securityTool    = "/usr/bin/security"
	errSecNotFound  = 44 // security(1) exit status for a missing item
)

var errNoKey = errors.New("no key in Keychain")

func keychainGet(account string) ([]byte, error) {
	out, err := exec.Command(securityTool, "find-generic-password", "-s", keychainService, "-a", account, "-w").Output()
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			return nil, fmt.Errorf("read Keychain: %w", err)
		}
		if exitErr.ExitCode() == errSecNotFound {
			return nil, errNoKey
		}
		return nil, fmt.Errorf("read Keychain: %s", bytes.TrimSpace(exitErr.Stderr))
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(out)))
	if err != nil || len(key) != keySize {
		return nil, fmt.Errorf("Keychain item %s/%s is not a tantalum key", keychainService, account)
	}
	return key, nil
}

func keychainAdd(account string, key []byte) error {
	// `security -i` reads the command from stdin, which keeps the key out of
	// argv where any local process could see it via ps.
	if strings.ContainsAny(account, "\"\\\n") {
		return fmt.Errorf("vault path %q must not contain quotes, backslashes or newlines", account)
	}
	cmd := exec.Command(securityTool, "-i")
	cmd.Stdin = strings.NewReader(fmt.Sprintf("add-generic-password -s %s -a \"%s\" -w %s\n",
		keychainService, account, base64.StdEncoding.EncodeToString(key)))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("write Keychain: %v: %s", err, bytes.TrimSpace(out))
	}
	return nil
}
