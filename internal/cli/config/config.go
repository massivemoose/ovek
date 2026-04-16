package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	DefaultHost     = "http://brain.localhost"
	dirPermissions  = 0o700
	filePermissions = 0o600
)

var ErrNotFound = errors.New("config not found")

type Config struct {
	Host   string `json:"host"`
	APIKey string `json:"apiKey"`
}

type Store struct {
	rootDir string
}

func NewStore(rootDir string) *Store {
	return &Store{rootDir: strings.TrimSpace(rootDir)}
}

func (store *Store) Path() (string, error) {
	rootDir := store.rootDir
	if rootDir == "" {
		var err error
		rootDir, err = os.UserConfigDir()
		if err != nil {
			return "", fmt.Errorf("locate user config directory: %w", err)
		}
	}

	return filepath.Join(rootDir, "alces", "config.json"), nil
}

func (store *Store) Load() (Config, error) {
	configPath, err := store.Path()
	if err != nil {
		return Config{}, err
	}

	payload, err := os.ReadFile(configPath)
	if errors.Is(err, os.ErrNotExist) {
		return Config{}, ErrNotFound
	}
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(payload, &cfg); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}

	cfg.Host = normalizeHost(cfg.Host)
	cfg.APIKey = strings.TrimSpace(cfg.APIKey)

	return cfg, nil
}

func (store *Store) Save(cfg Config) error {
	configPath, err := store.Path()
	if err != nil {
		return err
	}

	cfg.Host = normalizeHost(cfg.Host)
	cfg.APIKey = strings.TrimSpace(cfg.APIKey)

	if err := os.MkdirAll(filepath.Dir(configPath), dirPermissions); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	if err := os.Chmod(filepath.Dir(configPath), dirPermissions); err != nil {
		return fmt.Errorf("set config directory permissions: %w", err)
	}

	payload, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	payload = append(payload, '\n')

	if err := os.WriteFile(configPath, payload, filePermissions); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	if err := os.Chmod(configPath, filePermissions); err != nil {
		return fmt.Errorf("set config file permissions: %w", err)
	}

	return nil
}

func (store *Store) Clear() error {
	configPath, err := store.Path()
	if err != nil {
		return err
	}

	if err := os.Remove(configPath); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("remove config: %w", err)
	}

	return nil
}

func normalizeHost(host string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return DefaultHost
	}

	return strings.TrimRight(host, "/")
}
