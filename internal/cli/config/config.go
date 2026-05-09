package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	DefaultHost     = "http://brain.localhost"
	DefaultProfile  = "default"
	dirPermissions  = 0o700
	filePermissions = 0o600
)

var ErrNotFound = errors.New("config not found")
var ErrProfileNotFound = errors.New("profile not found")

type Profile struct {
	Host   string `json:"host"`
	APIKey string `json:"apiKey"`
}

type Config struct {
	ActiveProfile string             `json:"activeProfile"`
	Profiles      map[string]Profile `json:"profiles"`
}

type legacyConfig struct {
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

	return filepath.Join(rootDir, "ovek", "config.json"), nil
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
	if err := json.Unmarshal(payload, &cfg); err == nil && len(cfg.Profiles) > 0 {
		return normalizeConfig(cfg), nil
	}

	var legacy legacyConfig
	if err := json.Unmarshal(payload, &legacy); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}

	if strings.TrimSpace(legacy.Host) == "" && strings.TrimSpace(legacy.APIKey) == "" {
		return Config{}, fmt.Errorf("decode config: invalid config schema")
	}

	return normalizeConfig(Config{
		ActiveProfile: DefaultProfile,
		Profiles: map[string]Profile{
			DefaultProfile: {
				Host:   legacy.Host,
				APIKey: legacy.APIKey,
			},
		},
	}), nil
}

func (store *Store) Save(cfg Config) error {
	configPath, err := store.Path()
	if err != nil {
		return err
	}

	cfg = normalizeConfig(cfg)

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

func (store *Store) SaveProfile(profileName string, profile Profile, makeActive bool) error {
	cfg, err := store.Load()
	if errors.Is(err, ErrNotFound) {
		cfg = Config{Profiles: map[string]Profile{}}
	} else if err != nil {
		return err
	}

	profileName = normalizeProfileName(profileName)
	if cfg.Profiles == nil {
		cfg.Profiles = map[string]Profile{}
	}
	cfg.Profiles[profileName] = normalizeProfile(profile)
	if makeActive || cfg.ActiveProfile == "" {
		cfg.ActiveProfile = profileName
	}

	return store.Save(cfg)
}

func (store *Store) LoadProfile(profileName string) (string, Profile, error) {
	cfg, err := store.Load()
	if err != nil {
		return "", Profile{}, err
	}

	profileName = strings.TrimSpace(profileName)
	if profileName == "" {
		profileName = strings.TrimSpace(cfg.ActiveProfile)
	}
	if profileName == "" {
		return "", Profile{}, ErrProfileNotFound
	}

	profile, ok := cfg.Profiles[profileName]
	if !ok {
		return "", Profile{}, ErrProfileNotFound
	}

	return profileName, normalizeProfile(profile), nil
}

func (store *Store) SetActiveProfile(profileName string) error {
	cfg, err := store.Load()
	if err != nil {
		return err
	}

	profileName = normalizeProfileName(profileName)
	if _, ok := cfg.Profiles[profileName]; !ok {
		return ErrProfileNotFound
	}
	cfg.ActiveProfile = profileName
	return store.Save(cfg)
}

func (store *Store) RemoveProfile(profileName string) error {
	cfg, err := store.Load()
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}

	profileName = strings.TrimSpace(profileName)
	if profileName == "" {
		profileName = strings.TrimSpace(cfg.ActiveProfile)
	}
	if profileName == "" {
		return nil
	}

	delete(cfg.Profiles, profileName)
	if len(cfg.Profiles) == 0 {
		return store.Clear()
	}
	if cfg.ActiveProfile == profileName {
		names := profileNames(cfg)
		cfg.ActiveProfile = names[0]
	}

	return store.Save(cfg)
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

func normalizeConfig(cfg Config) Config {
	if cfg.Profiles == nil {
		cfg.Profiles = map[string]Profile{}
	}

	normalizedProfiles := make(map[string]Profile, len(cfg.Profiles))
	for name, profile := range cfg.Profiles {
		name = normalizeProfileName(name)
		normalizedProfiles[name] = normalizeProfile(profile)
	}
	cfg.Profiles = normalizedProfiles

	if strings.TrimSpace(cfg.ActiveProfile) == "" && len(cfg.Profiles) == 1 {
		cfg.ActiveProfile = profileNames(cfg)[0]
	}
	cfg.ActiveProfile = strings.TrimSpace(cfg.ActiveProfile)
	if cfg.ActiveProfile != "" {
		if _, ok := cfg.Profiles[cfg.ActiveProfile]; !ok {
			names := profileNames(cfg)
			if len(names) > 0 {
				cfg.ActiveProfile = names[0]
			} else {
				cfg.ActiveProfile = ""
			}
		}
	}

	return cfg
}

func normalizeProfile(profile Profile) Profile {
	profile.Host = normalizeHost(profile.Host)
	profile.APIKey = strings.TrimSpace(profile.APIKey)
	return profile
}

func normalizeProfileName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return DefaultProfile
	}
	return name
}

func normalizeHost(host string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return DefaultHost
	}

	return strings.TrimRight(host, "/")
}

func profileNames(cfg Config) []string {
	names := make([]string, 0, len(cfg.Profiles))
	for name := range cfg.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
