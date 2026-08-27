package openrouter

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
	contents, err := os.ReadFile(filepath.Join("..", "..", "testdata", "openrouter-models.json"))
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := Parse(bytes.NewReader(contents), time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Models) != 4 {
		t.Fatalf("model count = %d", len(catalog.Models))
	}

	byID := make(map[string]model.Candidate)
	for _, candidate := range catalog.Models {
		byID[candidate.ID] = candidate
	}
	stealth := byID["openrouter/quasar-alpha"]
	if len(stealth.Evidence) != 1 || stealth.Evidence[0].Class != model.ClassStealth {
		t.Fatalf("stealth evidence = %#v", stealth.Evidence)
	}
	if stealth.Tools == nil || !*stealth.Tools {
		t.Fatal("fixture tool support was not normalized")
	}
	if byID["openrouter/free"].Kind != model.KindRouter {
		t.Fatalf("free router kind = %q", byID["openrouter/free"].Kind)
	}
	paid := byID["deepseek/deepseek-v4-flash-vision-exp"]
	if len(paid.Prices) != 6 {
		t.Fatalf("override price count = %d", len(paid.Prices))
	}
	if paid.Prices[3].Tier == "" {
		t.Fatal("override tier condition was discarded")
	}
	free := byID["dots-studio/dots-3-note-preview:free"]
	if len(free.PriceErrors) != 0 || len(free.Prices) != 2 {
		t.Fatalf("free pricing = %#v, errors = %#v", free.Prices, free.PriceErrors)
	}
}

func TestMalformedCandidatePriceDoesNotBecomeSourceFailure(t *testing.T) {
	t.Parallel()
	contents := []byte(`{"data":[{"id":"example/model","name":"Example","created":1,"context_length":131072,"architecture":{"input_modalities":["text"],"output_modalities":["text"]},"pricing":{"prompt":"invalid","completion":"0"},"supported_parameters":["tools"]}]}`)
	catalog, err := Parse(bytes.NewReader(contents), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Models) != 1 || len(catalog.Models[0].PriceErrors) != 1 {
		t.Fatalf("parsed models = %#v", catalog.Models)
	}
}
