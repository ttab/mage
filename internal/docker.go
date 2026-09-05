package internal

import (
	"fmt"
	"strings"
	"time"

	"github.com/magefile/mage/sh"
)

func StopContainerIfExists(name string) error {
	state, err := inspectContainer(name)
	if err != nil {
		return err
	}

	if !state.exists {
		return nil
	}

	if state.running {
		err = sh.Run("docker", "stop", name)
		if err != nil {
			return fmt.Errorf("stop container: %w", err)
		}
	}

	// A container started with --rm is removed asynchronously once it has
	// stopped, and it keeps its name until that's done. Wait for the name
	// to actually be free, or the next "docker run" fails with a name
	// conflict.
	for range 100 {
		state, err := inspectContainer(name)
		if err != nil {
			return err
		}

		if !state.exists {
			return nil
		}

		if !state.running && !state.autoRemove {
			return nil
		}

		time.Sleep(100 * time.Millisecond)
	}

	return fmt.Errorf("timed out waiting for the %q container to stop", name)
}

type containerState struct {
	exists     bool
	running    bool
	autoRemove bool
}

func inspectContainer(name string) (containerState, error) {
	out, err := OutputSilent("docker", "inspect",
		"--format", "{{.State.Running}} {{.HostConfig.AutoRemove}}", name)
	if err != nil {
		// Inspect fails if the container doesn't exist. We have no way
		// of telling that apart from a docker that isn't there at all,
		// but the latter fails loudly enough in the run that follows.
		return containerState{}, nil
	}

	state := containerState{exists: true}

	_, err = fmt.Sscanf(out, "%t %t", &state.running, &state.autoRemove)
	if err != nil {
		return containerState{}, fmt.Errorf(
			"parse the state %q of the %q container: %w",
			out, name, err)
	}

	return state, nil
}

// RunningContainerNames returns the names of the currently running containers.
func RunningContainerNames() ([]string, error) {
	out, err := OutputSilent("docker", "ps", "--format", "{{.Names}}")
	if err != nil {
		return nil, fmt.Errorf("list containers: %w", err)
	}

	if out == "" {
		return nil, nil
	}

	return strings.Split(out, "\n"), nil
}
