package source

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/Mtrya/spolia/internal/model"
)

const maxCatalogSize = 64 << 20

type Catalog struct {
	Source         string
	FetchedAt      time.Time
	Models         []model.Candidate
	InvalidRecords map[string]int
}

type Adapter interface {
	Name() string
	Fetch(context.Context, string) (Catalog, error)
}

func Fetch(client *http.Client, request *http.Request) ([]byte, error) {
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("request catalog: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("catalog request returned HTTP %d", response.StatusCode)
	}
	contents, err := io.ReadAll(io.LimitReader(response.Body, maxCatalogSize+1))
	if err != nil {
		return nil, fmt.Errorf("read catalog: %w", err)
	}
	if len(contents) > maxCatalogSize {
		return nil, fmt.Errorf("catalog exceeds %d bytes", maxCatalogSize)
	}
	return contents, nil
}
