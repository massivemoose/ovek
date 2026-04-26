package main

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadSecretCipherUsesEnvOverride(t *testing.T) {
	key := strings.Repeat("a", secretKeyLength)
	t.Setenv(secretsKeyEnv, base64.StdEncoding.EncodeToString([]byte(key)))
	dataDir := t.TempDir()

	cipherBox, err := loadSecretCipher(dataDir)
	if err != nil {
		t.Fatalf("expected secret cipher load to succeed, got error: %v", err)
	}

	encrypted, err := cipherBox.Encrypt("super-secret")
	if err != nil {
		t.Fatalf("expected encrypt to succeed, got error: %v", err)
	}
	decrypted, err := cipherBox.Decrypt(encrypted)
	if err != nil {
		t.Fatalf("expected decrypt to succeed, got error: %v", err)
	}
	if decrypted != "super-secret" {
		t.Fatalf("expected decrypted secret %q, got %q", "super-secret", decrypted)
	}
	if _, err := os.Stat(secretKeyPath(dataDir)); !os.IsNotExist(err) {
		t.Fatalf("expected env override not to create local key file, got error %v", err)
	}
}

func TestLoadSecretCipherGeneratesLocalKeyWithRestrictedPermissions(t *testing.T) {
	dataDir := t.TempDir()

	firstCipher, err := loadSecretCipher(dataDir)
	if err != nil {
		t.Fatalf("expected first secret cipher load to succeed, got error: %v", err)
	}
	encrypted, err := firstCipher.Encrypt("persisted-secret")
	if err != nil {
		t.Fatalf("expected encrypt to succeed, got error: %v", err)
	}

	keyPath := secretKeyPath(dataDir)
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("expected local key file to exist, got error: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("expected key file permissions 0600, got %o", info.Mode().Perm())
	}
	dirInfo, err := os.Stat(filepath.Dir(keyPath))
	if err != nil {
		t.Fatalf("expected key directory to exist, got error: %v", err)
	}
	if dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("expected key directory permissions 0700, got %o", dirInfo.Mode().Perm())
	}

	secondCipher, err := loadSecretCipher(dataDir)
	if err != nil {
		t.Fatalf("expected second secret cipher load to succeed, got error: %v", err)
	}
	decrypted, err := secondCipher.Decrypt(encrypted)
	if err != nil {
		t.Fatalf("expected decrypt after reload to succeed, got error: %v", err)
	}
	if decrypted != "persisted-secret" {
		t.Fatalf("expected decrypted secret %q, got %q", "persisted-secret", decrypted)
	}
}

func TestParseSecretKeyRejectsInvalidLength(t *testing.T) {
	if _, err := parseSecretKey(base64.StdEncoding.EncodeToString([]byte("too-short"))); err == nil {
		t.Fatal("expected invalid secret key length to fail")
	}
}
