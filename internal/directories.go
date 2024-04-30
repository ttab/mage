package internal

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

func MustGetWD() string {
	cwd, err := os.Getwd()
	if err != nil {
		panic(fmt.Errorf("get current directory: %w", err))
	}

	return cwd
}

func StateDir() (string, error) {
	home, err := getDataHomeDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(home, "tt-mage"), nil
}

func getDataHomeDir() (string, error) {
	dir := os.Getenv("STATE_DIR")
	if dir != "" {
		return dir, nil
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("get home directory: %w", err)
	}

	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(homeDir, "Library"), nil
	case "windows":
		dir := os.Getenv("LocalAppData")
		if dir != "" {
			return dir, nil
		}
	default:
		dir = os.Getenv("XDG_DATA_HOME")
		if dir != "" {
			return dir, nil
		}

		return filepath.Join(homeDir,
			".local", "share"), nil
	}

	return filepath.Join(homeDir, "localstate"), nil
}
