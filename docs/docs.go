// Package docs provides mage targets that keep a repository's
// documentation honest.
package docs

import (
	"fmt"
	"os"

	"github.com/ttab/mage/doclint"
)

// Links checks that every relative link and heading anchor in the
// repository's markdown files resolves. Run from the repository root.
func Links() error {
	problems, err := doclint.CheckDir(".")
	if err != nil {
		return fmt.Errorf("check documentation links: %w", err)
	}

	for _, p := range problems {
		fmt.Fprintln(os.Stderr, p)
	}

	if len(problems) > 0 {
		return fmt.Errorf("%d broken documentation links", len(problems))
	}

	return nil
}
