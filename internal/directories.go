package internal

import (
	"fmt"
	"os"
)

func MustGetWD() string {
	cwd, err := os.Getwd()
	if err != nil {
		panic(fmt.Errorf("get current directory: %w", err))
	}

	return cwd
}
