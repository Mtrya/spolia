package openrouter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Mtrya/llmloot/internal/model"
	"github.com/Mtrya/llmloot/internal/source"
)

const defaultEndpoint = "https://openrouter.ai/api/v1/models"

type Adapter struct {
	client   *http.Client
	endpoint string
	now      func() time.Time
}

func New(client *http.Client) *Adapter {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &Adapter{client: client, endpoint: defaultEndpoint, now: time.Now}
}

func (adapter *Adapter) Name() string {
	return "openrouter"
}

func (adapter *Adapter) Fetch(ctx context.Context, apiKey string) (source.Catalog, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, adapter.endpoint, nil)
	if err != nil {
		return source.Catalog{}, fmt.Errorf("build OpenRouter request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+apiKey)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "llmloot/0.1")
	contents, err := source.Fetch(adapter.client, request)
	if err != nil {
		return source.Catalog{}, fmt.Errorf("OpenRouter: %w", err)
	}
	return Parse(bytes.NewReader(contents), adapter.now().UTC())
}

func Parse(reader *bytes.Reader, fetchedAt time.Time) (source.Catalog, error) {
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	contents := make([]byte, reader.Len())
	if _, err := reader.Read(contents); err != nil {
		return source.Catalog{}, fmt.Errorf("read OpenRouter catalog: %w", err)
	}
	if err := source.DecodeJSON(contents, &envelope); err != nil {
		return source.Catalog{}, fmt.Errorf("decode OpenRouter catalog: %w", err)
	}
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return source.Catalog{}, fmt.Errorf("OpenRouter catalog has no data array")
	}
	var records []json.RawMessage
	if err := source.DecodeJSON(envelope.Data, &records); err != nil {
		return source.Catalog{}, fmt.Errorf("decode OpenRouter models: %w", err)
	}

	catalog := source.Catalog{Source: "openrouter", FetchedAt: fetchedAt, InvalidRecords: make(map[string]int)}
	for _, record := range records {
		candidate, err := parseRecord(record)
		if err != nil {
			catalog.InvalidRecords["malformed_record"]++
			continue
		}
		catalog.Models = append(catalog.Models, candidate)
	}
	if len(records) > 0 && len(catalog.Models) == 0 {
		return source.Catalog{}, fmt.Errorf("OpenRouter catalog has no usable model records")
	}
	return catalog, nil
}

type rawModel struct {
	ID                  string                     `json:"id"`
	Name                string                     `json:"name"`
	Description         string                     `json:"description"`
	Created             json.Number                `json:"created"`
	ContextLength       json.Number                `json:"context_length"`
	ExpirationDate      string                     `json:"expiration_date"`
	AliasTarget         json.RawMessage            `json:"alias_target"`
	Pricing             map[string]json.RawMessage `json:"pricing"`
	SupportedParameters *[]string                  `json:"supported_parameters"`
	Architecture        struct {
		Tokenizer        string   `json:"tokenizer"`
		InputModalities  []string `json:"input_modalities"`
		OutputModalities []string `json:"output_modalities"`
	} `json:"architecture"`
	TopProvider struct {
		ContextLength       json.Number `json:"context_length"`
		MaxCompletionTokens json.Number `json:"max_completion_tokens"`
	} `json:"top_provider"`
	Reasoning json.RawMessage `json:"reasoning"`
}

func parseRecord(record json.RawMessage) (model.Candidate, error) {
	var raw rawModel
	if err := source.DecodeJSON(record, &raw); err != nil {
		return model.Candidate{}, err
	}
	if strings.TrimSpace(raw.ID) == "" {
		return model.Candidate{}, fmt.Errorf("model has no id")
	}
	candidate := model.Candidate{
		Source:           "openrouter",
		ID:               raw.ID,
		DisplayName:      raw.Name,
		Kind:             model.KindModel,
		Protocol:         "openai_chat",
		InputModalities:  normalizedStrings(raw.Architecture.InputModalities),
		OutputModalities: normalizedStrings(raw.Architecture.OutputModalities),
		Capabilities:     make(map[string]bool),
	}
	if candidate.DisplayName == "" {
		candidate.DisplayName = candidate.ID
	}

	if timestamp, err := parseUnix(raw.Created); err == nil {
		candidate.CreatedAt = timestamp
	}
	if contextWindow, err := parsePositiveInt(raw.ContextLength); err == nil {
		candidate.ContextWindow = contextWindow
	}
	if topContext, err := parsePositiveInt(raw.TopProvider.ContextLength); err == nil && (candidate.ContextWindow == 0 || topContext < candidate.ContextWindow) {
		candidate.ContextWindow = topContext
	}
	if maximum, err := parsePositiveInt(raw.TopProvider.MaxCompletionTokens); err == nil {
		candidate.MaxOutput = model.Int64(maximum)
	}
	if raw.ExpirationDate != "" {
		if expiration, err := time.Parse("2006-01-02", raw.ExpirationDate); err == nil {
			expiration = expiration.Add(24 * time.Hour)
			candidate.ExpiresAt = &expiration
		}
	}

	if hasValue(raw.AliasTarget) {
		candidate.Kind = model.KindAlias
	} else if strings.EqualFold(raw.Architecture.Tokenizer, "router") {
		candidate.Kind = model.KindRouter
	} else if raw.ID == "openrouter/free" || raw.ID == "openrouter/auto" {
		candidate.Kind = model.KindUtility
	}

	if raw.SupportedParameters != nil {
		supported := contains(*raw.SupportedParameters, "tools")
		candidate.Tools = model.Bool(supported)
		if contains(*raw.SupportedParameters, "reasoning") || contains(*raw.SupportedParameters, "include_reasoning") {
			candidate.Capabilities["reasoning"] = true
		}
	}
	if hasValue(raw.Reasoning) {
		candidate.Capabilities["reasoning"] = true
		var reasoning struct {
			Mandatory bool `json:"mandatory"`
		}
		if source.DecodeJSON(raw.Reasoning, &reasoning) == nil && reasoning.Mandatory {
			candidate.Capabilities["always_thinking"] = true
		}
	}
	addModalityCapabilities(&candidate)
	candidate.Prices, candidate.PriceErrors = parsePricing(raw.Pricing)
	candidate.Evidence = stealthEvidence(raw.ID, raw.Description)
	return candidate, nil
}

func parsePricing(pricing map[string]json.RawMessage) ([]model.Price, []model.PriceError) {
	var prices []model.Price
	var priceErrors []model.PriceError
	keys := sortedKeys(pricing)
	for _, dimension := range keys {
		if dimension == "overrides" {
			continue
		}
		price, err := scalarPrice(dimension, pricing[dimension], "")
		if err != nil {
			priceErrors = append(priceErrors, model.PriceError{Dimension: dimension, Code: "malformed_price"})
			continue
		}
		prices = append(prices, price)
	}

	if rawOverrides, exists := pricing["overrides"]; exists {
		var overrides []map[string]json.RawMessage
		if err := source.DecodeJSON(rawOverrides, &overrides); err != nil {
			priceErrors = append(priceErrors, model.PriceError{Dimension: "overrides", Code: "malformed_tiers"})
			return prices, priceErrors
		}
		for _, override := range overrides {
			conditions := make(map[string]json.RawMessage)
			for key, value := range override {
				if isOverrideCondition(key) {
					conditions[key] = value
				}
			}
			conditionJSON, err := json.Marshal(conditions)
			if err != nil {
				priceErrors = append(priceErrors, model.PriceError{Dimension: "overrides", Code: "malformed_tiers"})
				continue
			}
			for _, dimension := range sortedKeys(override) {
				if isOverrideCondition(dimension) {
					continue
				}
				price, err := scalarPrice(dimension, override[dimension], string(conditionJSON))
				if err != nil {
					priceErrors = append(priceErrors, model.PriceError{Dimension: dimension, Code: "malformed_price"})
					continue
				}
				prices = append(prices, price)
			}
		}
	}
	return prices, priceErrors
}

func scalarPrice(dimension string, raw json.RawMessage, tier string) (model.Price, error) {
	value, err := decimalText(raw)
	if err != nil {
		return model.Price{}, err
	}
	decimal, err := model.ParseDecimal(value)
	if err != nil || decimal.Sign() < 0 {
		return model.Price{}, fmt.Errorf("invalid price")
	}
	return model.Price{Dimension: normalizeDimension(dimension), Value: value, Unit: openRouterUnit(dimension), Currency: "USD", Tier: tier}, nil
}

func decimalText(raw json.RawMessage) (string, error) {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text, nil
	}
	var number json.Number
	if err := source.DecodeJSON(raw, &number); err == nil && number.String() != "" {
		return number.String(), nil
	}
	return "", fmt.Errorf("price is not a decimal")
}

func openRouterUnit(dimension string) string {
	switch normalizeDimension(dimension) {
	case "request", "web_search":
		return "per_request"
	case "image_output":
		return "per_image"
	default:
		return "per_token"
	}
}

func normalizeDimension(dimension string) string {
	switch strings.ToLower(dimension) {
	case "input":
		return "prompt"
	case "output":
		return "completion"
	default:
		return strings.ToLower(dimension)
	}
}

func isOverrideCondition(key string) bool {
	switch key {
	case "utc_days", "utc_start", "utc_end", "min_prompt_tokens", "max_prompt_tokens", "min_completion_tokens", "max_completion_tokens":
		return true
	default:
		return false
	}
}

func stealthEvidence(id, description string) []model.Evidence {
	if strings.HasPrefix(strings.ToLower(id), "stealth/") {
		return []model.Evidence{{Class: model.ClassStealth, Kind: "official_namespace", Field: "id", Value: "stealth/"}}
	}
	lower := strings.ToLower(description)
	phrases := []string{"cloaked model", "anonymous model", "undisclosed model", "provided anonymously", "identity will be revealed"}
	for _, phrase := range phrases {
		if strings.Contains(lower, phrase) {
			return []model.Evidence{{Class: model.ClassStealth, Kind: "official_description", Field: "description", Value: phrase}}
		}
	}
	return nil
}

func parseUnix(value json.Number) (time.Time, error) {
	integer, err := strconv.ParseInt(value.String(), 10, 64)
	if err != nil || integer <= 0 {
		return time.Time{}, fmt.Errorf("invalid timestamp")
	}
	return time.Unix(integer, 0).UTC(), nil
}

func parsePositiveInt(value json.Number) (int64, error) {
	integer, err := strconv.ParseInt(value.String(), 10, 64)
	if err != nil || integer <= 0 {
		return 0, fmt.Errorf("invalid positive integer")
	}
	return integer, nil
}

func hasValue(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	return trimmed != "" && trimmed != "null" && trimmed != "false"
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func normalizedStrings(values []string) []string {
	result := append([]string(nil), values...)
	for index := range result {
		result[index] = strings.ToLower(result[index])
	}
	sort.Strings(result)
	return result
}

func sortedKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func addModalityCapabilities(candidate *model.Candidate) {
	for _, modality := range append(append([]string(nil), candidate.InputModalities...), candidate.OutputModalities...) {
		switch modality {
		case "image", "audio", "video":
			candidate.Capabilities[modality] = true
		}
	}
}
