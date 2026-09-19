package main

import (
	"os"
	"path/filepath"
	"testing"
)

func withTempSSHKey(t *testing.T) {
	t.Helper()
	tempDir, err := os.MkdirTemp("", "invoke_sshkey_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	oldPath := sshKeyPath
	oldKey := sshMachineKeyData
	sshKeyPath = filepath.Join(tempDir, ".invoke_sshkey_test")
	sshMachineKeyData = nil

	t.Cleanup(func() {
		sshKeyPath = oldPath
		sshMachineKeyData = oldKey
		os.RemoveAll(tempDir)
	})
}

func TestSSHPasswordEncryptionDecryption(t *testing.T) {
	withTempSSHKey(t)

	plain := "SecretP@ssw0rd!2026"
	encrypted := encryptSSHPassword(plain)

	if encrypted == "" {
		t.Fatalf("expected non-empty encrypted string")
	}
	if encrypted == plain {
		t.Fatalf("encrypted string should not match plaintext")
	}

	decrypted := decryptSSHPassword(encrypted)
	if decrypted != plain {
		t.Errorf("expected decrypted text '%s', got '%s'", plain, decrypted)
	}
}

func TestSSHPasswordDecryptInvalid(t *testing.T) {
	withTempSSHKey(t)

	dec := decryptSSHPassword("invalid-base64-string!")
	if dec != "" {
		t.Errorf("expected empty string for invalid ciphertext, got '%s'", dec)
	}
}
