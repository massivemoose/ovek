package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	secretsKeyEnv      = "OVEK_SECRETS_KEY"
	secretsKeyDirName  = "secrets"
	secretsKeyFileName = "master.key"
	secretCipherPrefix = "v1:"
	secretKeyLength    = 32
)

type secretCipher struct {
	key []byte
}

func loadSecretCipher(dataDir string) (secretCipher, error) {
	if keyValue := strings.TrimSpace(os.Getenv(secretsKeyEnv)); keyValue != "" {
		key, err := parseSecretKey(keyValue)
		if err != nil {
			return secretCipher{}, fmt.Errorf("parse %s: %w", secretsKeyEnv, err)
		}

		return secretCipher{key: key}, nil
	}

	key, err := readOrCreateLocalSecretKey(secretKeyPath(dataDir))
	if err != nil {
		return secretCipher{}, err
	}

	return secretCipher{key: key}, nil
}

func secretKeyPath(dataDir string) string {
	return filepath.Join(dataDir, secretsKeyDirName, secretsKeyFileName)
}

func readOrCreateLocalSecretKey(path string) ([]byte, error) {
	payload, err := os.ReadFile(path)
	if err == nil {
		if chmodErr := os.Chmod(path, 0o600); chmodErr != nil {
			return nil, fmt.Errorf("set secret key file permissions: %w", chmodErr)
		}
		return parseSecretKey(strings.TrimSpace(string(payload)))
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read secret key file: %w", err)
	}

	key := make([]byte, secretKeyLength)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate secret key: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create secret key directory: %w", err)
	}
	if err := os.Chmod(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("set secret key directory permissions: %w", err)
	}

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return readOrCreateLocalSecretKey(path)
	}
	if err != nil {
		return nil, fmt.Errorf("create secret key file: %w", err)
	}
	defer file.Close()

	if _, err := fmt.Fprintln(file, base64.StdEncoding.EncodeToString(key)); err != nil {
		return nil, fmt.Errorf("write secret key file: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return nil, fmt.Errorf("set secret key file permissions: %w", err)
	}

	return key, nil
}

func parseSecretKey(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, errors.New("secret key is empty")
	}

	decoders := []func(string) ([]byte, error){
		base64.StdEncoding.DecodeString,
		base64.RawStdEncoding.DecodeString,
		hex.DecodeString,
	}
	for _, decode := range decoders {
		key, err := decode(value)
		if err == nil && len(key) == secretKeyLength {
			return key, nil
		}
	}
	if len([]byte(value)) == secretKeyLength {
		return []byte(value), nil
	}

	return nil, fmt.Errorf("secret key must decode to %d bytes", secretKeyLength)
}

func (cipherBox secretCipher) Encrypt(plaintext string) (string, error) {
	gcm, err := cipherBox.gcm()
	if err != nil {
		return "", err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("generate secret nonce: %w", err)
	}

	ciphertext := gcm.Seal(nil, nonce, []byte(plaintext), nil)
	payload := append(nonce, ciphertext...)
	return secretCipherPrefix + base64.RawStdEncoding.EncodeToString(payload), nil
}

func (cipherBox secretCipher) Decrypt(encoded string) (string, error) {
	encoded = strings.TrimSpace(encoded)
	if !strings.HasPrefix(encoded, secretCipherPrefix) {
		return "", errors.New("unsupported secret encoding")
	}

	payload, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(encoded, secretCipherPrefix))
	if err != nil {
		return "", fmt.Errorf("decode encrypted secret: %w", err)
	}

	gcm, err := cipherBox.gcm()
	if err != nil {
		return "", err
	}
	if len(payload) < gcm.NonceSize() {
		return "", errors.New("encrypted secret payload is too short")
	}

	nonce := payload[:gcm.NonceSize()]
	ciphertext := payload[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt secret: %w", err)
	}

	return string(plaintext), nil
}

func (cipherBox secretCipher) gcm() (cipher.AEAD, error) {
	if len(cipherBox.key) != secretKeyLength {
		return nil, fmt.Errorf("secret key must be %d bytes", secretKeyLength)
	}

	block, err := aes.NewCipher(cipherBox.key)
	if err != nil {
		return nil, fmt.Errorf("create secret cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create secret gcm: %w", err)
	}

	return gcm, nil
}
