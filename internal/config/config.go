package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const envConfig = "RQAMS_CLI_CONFIG"

// Profile stores one isolated login/workspace context.
type Profile struct {
	BaseURL     string `json:"base_url,omitempty"`
	Username    string `json:"username,omitempty"`
	Password    string `json:"password,omitempty"`
	UserID      string `json:"user_id,omitempty"`
	SID         string `json:"sid,omitempty"`
	WorkspaceID string `json:"workspace_id,omitempty"`
	Plaintext   bool   `json:"plaintext_credentials,omitempty"`
}

// Config stores local rqams CLI state.
type Config struct {
	BaseURL     string             `json:"base_url"`
	Username    string             `json:"username,omitempty"`
	Password    string             `json:"password,omitempty"`
	UserID      string             `json:"user_id,omitempty"`
	SID         string             `json:"sid,omitempty"`
	WorkspaceID string             `json:"workspace_id,omitempty"`
	Plaintext   bool               `json:"plaintext_credentials,omitempty"`
	Profile     string             `json:"profile,omitempty"`
	Profiles    map[string]Profile `json:"profiles,omitempty"`
}

// Load reads config from the local config file.
func Load() (Config, error) {
	path, err := Path()
	if err != nil {
		return Config{}, err
	}
	cfg, err := loadFileLocked(path)
	if err != nil {
		return Config{}, err
	}
	cfg = SelectProfile(cfg, cfg.Profile)
	return cfg, nil
}

// Save persists non-empty local config fields.
func Save(cfg Config) error {
	path, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return withConfigLock(path, func() error {
		current, err := loadFileRaw(path)
		if err != nil {
			return err
		}
		cfg = mergeForSave(current, cfg)
		raw, err := json.MarshalIndent(cfg, "", "  ")
		if err != nil {
			return err
		}
		return os.WriteFile(path, append(raw, '\n'), 0o600)
	})
}

// SelectProfile returns cfg with profile fields promoted into the active config.
func SelectProfile(cfg Config, profile string) Config {
	profile = strings.TrimSpace(profile)
	if profile == "" {
		return cfg
	}
	cfg.Profile = profile
	if cfg.Profiles == nil {
		return cfg
	}
	stored, ok := cfg.Profiles[profile]
	if !ok {
		return cfg
	}
	cfg.BaseURL = stored.BaseURL
	cfg.Username = stored.Username
	cfg.Password = stored.Password
	cfg.UserID = stored.UserID
	cfg.SID = stored.SID
	cfg.WorkspaceID = stored.WorkspaceID
	cfg.Plaintext = stored.Plaintext
	return cfg
}

func mergeForSave(current Config, cfg Config) Config {
	if strings.TrimSpace(cfg.Profile) == "" {
		return cfg
	}
	if current.Profiles == nil {
		current.Profiles = map[string]Profile{}
	}
	if cfg.Profiles != nil {
		for name, profile := range cfg.Profiles {
			if strings.TrimSpace(name) != "" {
				current.Profiles[name] = profile
			}
		}
	}
	current.Profile = cfg.Profile
	current.BaseURL = cfg.BaseURL
	current.Username = cfg.Username
	current.Password = cfg.Password
	current.UserID = cfg.UserID
	current.SID = cfg.SID
	current.WorkspaceID = cfg.WorkspaceID
	current.Plaintext = cfg.Plaintext
	current.Profiles[cfg.Profile] = profileFromConfig(cfg)
	return current
}

func profileFromConfig(cfg Config) Profile {
	return Profile{
		BaseURL:     cfg.BaseURL,
		Username:    cfg.Username,
		Password:    cfg.Password,
		UserID:      cfg.UserID,
		SID:         cfg.SID,
		WorkspaceID: cfg.WorkspaceID,
		Plaintext:   cfg.Plaintext,
	}
}

func loadFileLocked(path string) (Config, error) {
	var cfg Config
	err := withConfigLock(path, func() error {
		loaded, err := loadFileRaw(path)
		if err != nil {
			return err
		}
		cfg = loaded
		return nil
	})
	if err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Path returns the local config path.
func Path() (string, error) {
	if override := os.Getenv(envConfig); strings.TrimSpace(override) != "" {
		return override, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "rqams-cli", "config.json"), nil
}

// RequireBaseURL validates and returns the AMS base URL.
func (cfg Config) RequireBaseURL() (string, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return "", errors.New("missing AMS base URL; run auth or set config base_url")
	}
	return strings.TrimRight(cfg.BaseURL, "/"), nil
}

func loadFile() (Config, error) {
	path, err := Path()
	if err != nil {
		return Config{}, err
	}
	return loadFileLocked(path)
}

func loadFileRaw(path string) (Config, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if legacyRaw, legacyErr := os.ReadFile(legacyPath()); legacyErr == nil {
			raw = legacyRaw
		} else {
			return Config{}, nil
		}
	} else if err != nil {
		return Config{}, err
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return Config{}, nil
	}
	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return Config{}, fmt.Errorf("invalid config JSON %s: %w", path, err)
	}
	return cfg, nil
}

func withConfigLock(path string, fn func() error) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	lockPath := path + ".lock"
	deadline := time.Now().Add(5 * time.Second)
	for {
		lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_, _ = fmt.Fprintf(lock, "%d\n", os.Getpid())
			_ = lock.Close()
			defer func() {
				_ = os.Remove(lockPath)
			}()
			return fn()
		}
		if !errors.Is(err, os.ErrExist) {
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for config lock %s", lockPath)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func legacyPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "rqamsc-demo", "config.json")
}
