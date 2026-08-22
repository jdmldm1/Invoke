package main

import (
	"testing"
)

func TestSSHPasswordEncryptionDecryption(t *testing.T) {
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
	dec := decryptSSHPassword("invalid-base64-string!")
	if dec != "" {
		t.Errorf("expected empty string for invalid ciphertext, got '%s'", dec)
	}
}

func TestItoaHelper(t *testing.T) {
	tests := map[int]string{
		0:    "0",
		22:   "22",
		8080: "8080",
		-42:  "-42",
	}

	for input, expected := range tests {
		got := itoa(input)
		if got != expected {
			t.Errorf("itoa(%d) = %s; expected %s", input, got, expected)
		}
	}
}
