package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestPathPrefersNewConfigEnv(t *testing.T) {
	t.Setenv("RQAMS_CLI_CONFIG", filepath.Join(t.TempDir(), "new.json"))

	path, err := Path()
	if err != nil {
		t.Fatalf("Path returned error: %v", err)
	}
	if filepath.Base(path) != "new.json" {
		t.Fatalf("expected new config env path, got %s", path)
	}
}

func TestProfilesAreSavedAndCanBeSelected(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("RQAMS_CLI_CONFIG", configPath)

	if err := Save(Config{
		Profile:     "acct-a-w1",
		BaseURL:     "https://example-a.test",
		Username:    "user-a",
		Password:    "pass-a",
		UserID:      "ua",
		SID:         "sid-a",
		WorkspaceID: "w1",
		Plaintext:   true,
	}); err != nil {
		t.Fatalf("Save first profile returned error: %v", err)
	}
	if err := Save(Config{
		Profile:     "acct-b-w2",
		BaseURL:     "https://example-b.test",
		Username:    "user-b",
		Password:    "pass-b",
		UserID:      "ub",
		SID:         "sid-b",
		WorkspaceID: "w2",
		Plaintext:   true,
	}); err != nil {
		t.Fatalf("Save second profile returned error: %v", err)
	}

	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	var saved Config
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatalf("saved config should be JSON: %v", err)
	}
	if len(saved.Profiles) != 2 {
		t.Fatalf("expected two saved profiles: %#v", saved.Profiles)
	}

	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	loaded = SelectProfile(loaded, "acct-a-w1")
	if loaded.Username != "user-a" || loaded.Password != "pass-a" || loaded.SID != "sid-a" || loaded.WorkspaceID != "w1" {
		t.Fatalf("profile acct-a-w1 was not selected: %#v", loaded)
	}
}
