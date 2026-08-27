package zenmux

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Mtrya/llmloot/internal/model"
)

func TestParseRealCatalogFixture(t *testing.T) {
	t.Parallel()
	contents, err := os.ReadFile(filepath.Join("..", "..", "testdata", "zenmux-models.json"))
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := Parse(bytes.NewReader(contents), time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Models) != 2 {
		t.Fatalf("model count = %d", len(catalog.Models))
	}

	byID := make(map[string]model.Candidate)
	for _, candidate := range catalog.Models {
		byID[candidate.ID] = candidate
	}
	free := byID["deepseek/deepseek-v4-flash-vision-exp-free"]
	if !free.CreatedAt.Equal(time.Date(2026, 8, 21, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("publication time = %s", free.CreatedAt)
	}
	if free.Tools != nil {
		t.Fatal("omitted tool metadata was turned into an explicit value")
	}
	if !free.Capabilities["reasoning"] {
		t.Fatal("explicit reasoning capability was discarded")
	}
	for _, price := range free.Prices {
		if price.Value != "0" || price.Unit != "perMTokens" || price.Currency != "USD" {
			t.Fatalf("free price = %#v", price)
		}
	}
	paid := byID["anthropic/claude-sonnet-4.5"]
	if len(paid.Prices) != 9 {
		t.Fatalf("tiered price count = %d", len(paid.Prices))
	}
	if paid.Prices[0].Tier == "" {
		t.Fatal("tier conditions were discarded")
	}
}
