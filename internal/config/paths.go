package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// Environment overrides, mostly so tests never touch the real home directory.
const (
	EnvConfigDir  = "WTM_CONFIG_DIR"
	EnvBackupsDir = "WTM_BACKUPS_DIR"
)

func Dir() (string, error) {
	if d := os.Getenv(EnvConfigDir); d != "" {
		return d, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home directory not found: %w", err)
	}
	return filepath.Join(home, ".config", "wtm"), nil
}

func Path() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

func BackupsDir() (string, error) {
	if d := os.Getenv(EnvBackupsDir); d != "" {
		return d, nil
	}
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "backups"), nil
}
