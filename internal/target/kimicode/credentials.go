package kimicode

import (
	"context"
	"fmt"
)

type Credentials struct {
	Document  Document
	Providers map[string]ProviderSpec
	Bootstrap map[string]string
}

func (credentials Credentials) Resolve(_ context.Context, sourceName, _ string) (string, error) {
	provider, exists := credentials.Providers[sourceName]
	if !exists {
		return "", fmt.Errorf("source %q has no Kimi Code provider", sourceName)
	}
	inspection := credentials.Document.Provider(provider)
	if inspection.Exists && inspection.Compatible && inspection.CredentialExists {
		return inspection.credential, nil
	}
	if credential := credentials.Bootstrap[sourceName]; credential != "" {
		return credential, nil
	}
	if inspection.Exists && !inspection.Compatible {
		return "", fmt.Errorf("source %q has an incompatible Kimi Code provider", sourceName)
	}
	return "", fmt.Errorf("source %q has no Kimi Code provider credential", sourceName)
}
