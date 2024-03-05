package internal

import (
	"fmt"
	"io"

	"github.com/magefile/mage/sh"
)

func StopContainerIfExists(name string) error {
	_, err := sh.Exec(nil, io.Discard, io.Discard,
		"docker", "inspect", name)
	if err != nil {
		return nil
	}

	err = sh.Run("docker", "stop", name)
	if err != nil {
		return fmt.Errorf("stop container: %w", err)
	}

	err = sh.Run("docker", "wait", name)
	if err != nil {
		return fmt.Errorf("wait for container to stop: %w", err)
	}

	return nil
}
