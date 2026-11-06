package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type fileConfig struct {
	Address          string `json:"address,omitempty"`
	AccessToken      string `json:"access_token,omitempty"`
	RefreshToken     string `json:"refresh_token,omitempty"`
	AccessExpiresAt  int64  `json:"access_expires_at,omitempty"`
	RefreshExpiresAt int64  `json:"refresh_expires_at,omitempty"`
	Username         string `json:"username,omitempty"`
}

func resolveConfigPath(flag string) (string, error) {
	if flag != "" {
		return flag, nil
	}
	if env := os.Getenv("FORGE_CONFIG"); env != "" {
		return env, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "forge", "config.json"), nil
}

func loadConfig(path string) (fileConfig, error) {
	resolved, err := resolveConfigPath(path)
	if err != nil {
		return fileConfig{}, err
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fileConfig{}, nil
		}
		return fileConfig{}, err
	}
	var cfg fileConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fileConfig{}, fmt.Errorf("parse config: %w", err)
	}
	return cfg, nil
}

func saveConfig(path string, cfg fileConfig) error {
	resolved, err := resolveConfigPath(path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(resolved), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp := resolved + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, resolved)
}
