package ia

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
)

// PromptForValue asks the user for a value and returns the result trimmed of
// whitespace.
func PromptForValue(prompt string, failOnEmpty bool) (string, error) {
	reader := bufio.NewReader(os.Stdin)

	fmt.Printf("%s: ", prompt)

	response, err := reader.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	response = strings.TrimSpace(response)
	if response == "" && failOnEmpty {
		return "", errors.New("empty response")
	}

	return response, nil
}

// PromptForValueWithDefault asks the user for a value and returns the default
// value if the response is empty.
func PromptForValueWithDefault(
	prompt string, defaultValue string,
) (string, error) {
	p := fmt.Sprintf("%s [default: %s]", prompt, defaultValue)

	response, err := PromptForValue(p, false)
	if err != nil {
		return "", err
	}

	if response == "" {
		return defaultValue, nil
	}

	return response, nil
}
