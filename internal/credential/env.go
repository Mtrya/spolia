package credential

import (
	"context"
	"fmt"
	"os"
	"strings"
)

type Environment struct{}

func (Environment) Resolve(_ context.Context, sourceName, environmentName string) (string, error) {
	value := strings.TrimSpace(os.Getenv(environmentName))
	if value == "" {
		return "", fmt.Errorf("source %q has no credential in %s", sourceName, environmentName)
	}
	return value, nil
}
