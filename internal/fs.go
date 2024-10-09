package internal

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
)

func FileExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("stat file: %w", err)
	}

	return true, nil
}

func DirectoryExists(path string) (bool, error) {
	stat, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("stat file: %w", err)
	}

	if !stat.IsDir() {
		return false, fmt.Errorf("%q is not a directory", path)
	}

	return true, nil
}

func EnsureDirectory(path string) error {
	stat, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		err := os.MkdirAll(path, 0o700)
		if err != nil {
			return fmt.Errorf("create directory: %w", err)
		}

		return nil
	} else if err != nil {
		return fmt.Errorf("stat directory: %w", err)
	}

	if !stat.IsDir() {
		return fmt.Errorf("%q is not a directory", path)
	}

	return nil
}
