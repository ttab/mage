package internal

import (
	"bytes"
	"io"
	"strings"

	"github.com/magefile/mage/sh"
)

// OutputSilent runs the command and returns the text from stdout. Stderr output
// is discarded.
func OutputSilent(cmd string, args ...string) (string, error) {
	buf := &bytes.Buffer{}
	_, err := sh.Exec(nil, buf, io.Discard, cmd, args...)
	return strings.TrimSuffix(buf.String(), "\n"), err
}
