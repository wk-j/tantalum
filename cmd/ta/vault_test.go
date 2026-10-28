package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// A vault written before environments existed must keep its secrets, as
// environment "default", and survive being rewritten in the new format.
func TestVersion1VaultLoadsAsDefaultEnvironment(t *testing.T) {
	v := newTestVault(t)
	plain, _ := json.Marshal(vaultPlaintextV1{Secrets: map[string]secret{"OLD": {Value: "old-value"}}})
	nonce := make([]byte, v.aead.NonceSize())
	raw, _ := json.Marshal(envelope{Version: 1, Nonce: nonce, Ciphertext: v.aead.Seal(nil, nonce, plain, vaultAAD(1))})
	if err := os.WriteFile(filepath.Join(v.dir, vaultFile), raw, 0o600); err != nil {
		t.Fatal(err)
	}

	envs, err := v.load()
	if err != nil {
		t.Fatal(err)
	}
	if got := envs[defaultEnv]["OLD"].Value; got != "old-value" {
		t.Fatalf("version 1 secret in %q = %q, want old-value", defaultEnv, got)
	}

	if err := v.set("prod", "NEW", "new-value"); err != nil {
		t.Fatal(err)
	}
	envs, err = v.load()
	if err != nil {
		t.Fatal(err)
	}
	if envs[defaultEnv]["OLD"].Value != "old-value" || envs["prod"]["NEW"].Value != "new-value" {
		t.Fatalf("after rewrite: %+v", envs)
	}
	var env envelope
	data, _ := os.ReadFile(filepath.Join(v.dir, vaultFile))
	json.Unmarshal(data, &env)
	if env.Version != vaultVersion {
		t.Fatalf("rewritten vault version = %d, want %d", env.Version, vaultVersion)
	}
}
