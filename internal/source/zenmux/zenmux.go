package zenmux

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

const defaultEndpoint = "https://zenmux.ai/api/v1/models"

type Adapter struct {
	client   *http.Client
	endpoint string
	now      func() time.Time
}

func New(client *http.Client) *Adapter {
	return NewAt(client, defaultEndpoint)
}

func NewAt(client *http.Client, endpoint string) *Adapter {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &Adapter{client: client, endpoint: endpoint, now: time.Now}
}

func (adapter *Adapter) Name() string {
	return "zenmux"
}

func (adapter *Adapter) Fetch(ctx context.Context, apiKey string) (source.Catalog, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, adapter.endpoint, nil)
	if err != nil {
		return source.Catalog{}, fmt.Errorf("build ZenMux request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+apiKey)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "llmloot/0.1")
	contents, err := source.Fetch(adapter.client, request)
	if err != nil {
		return source.Catalog{}, fmt.Errorf("ZenMux: %w", err)
	}
	return Parse(bytes.NewReader(contents), adapter.now().UTC())
}

func Parse(reader *bytes.Reader, fetchedAt time.Time) (source.Catalog, error) {
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	contents := make([]byte, reader.Len())
	if _, err := reader.Read(contents); err != nil {
		return source.Catalog{}, fmt.Errorf("read ZenMux catalog: %w", err)
	}
	if err := source.DecodeJSON(contents, &envelope); err != nil {
		return source.Catalog{}, fmt.Errorf("decode ZenMux catalog: %w", err)
	}
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return source.Catalog{}, fmt.Errorf("ZenMux catalog has no data array")
	}
	var records []json.RawMessage
	if err := source.DecodeJSON(envelope.Data, &records); err != nil {
		return source.Catalog{}, fmt.Errorf("decode ZenMux models: %w", err)
	}

	catalog := source.Catalog{Source: "zenmux", FetchedAt: fetchedAt, InvalidRecords: make(map[string]int)}
	for _, record := range records {
		candidate, err := parseRecord(record)
		if err != nil {
			catalog.InvalidRecords["malformed_record"]++
			continue
		}
		catalog.Models = append(catalog.Models, candidate)
	}
	if len(records) > 0 && len(catalog.Models) == 0 {
		return source.Catalog{}, fmt.Errorf("ZenMux catalog has no usable model records")
	}
	return catalog, nil
}

type rawModel struct {
	ID               string                     `json:"id"`
	Type             string                     `json:"type"`
	DisplayName      string                     `json:"display_name"`
	Created          json.RawMessage            `json:"created"`
	PublishTime      json.RawMessage            `json:"publish_time"`
	CreatedAt        string                     `json:"created_at"`
	ContextLength    json.Number                `json:"context_length"`
	InputModalities  []string                   `json:"input_modalities"`
	OutputModalities []string                   `json:"output_modalities"`
	Capabilities     map[string]json.RawMessage `json:"capabilities"`
	Pricings         map[string]json.RawMessage `json:"pricings"`
}

type rawPrice struct {
	Value      json.Number     `json:"value"`
	Unit       string          `json:"unit"`
	Currency   string          `json:"currency"`
	Conditions json.RawMessage `json:"conditions"`
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
		Source:           "zenmux",
		ID:               raw.ID,
		DisplayName:      raw.DisplayName,
		Kind:             model.KindModel,
		Protocol:         "openai_chat",
		InputModalities:  normalizedStrings(raw.InputModalities),
		OutputModalities: normalizedStrings(raw.OutputModalities),
		Capabilities:     make(map[string]bool),
	}
	if candidate.DisplayName == "" {
		candidate.DisplayName = candidate.ID
	}
	if raw.Type == "router" || raw.ID == "zenmux/auto" {
		candidate.Kind = model.KindRouter
	}

	candidate.CreatedAt = modelTimestamp(raw)
	if contextWindow, err := positiveInt(raw.ContextLength); err == nil {
		candidate.ContextWindow = contextWindow
	}
	for _, capability := range []string{"reasoning", "thinking"} {
		if rawValue, exists := raw.Capabilities[capability]; exists {
			var value bool
			if json.Unmarshal(rawValue, &value) == nil {
				candidate.Capabilities[capability] = value
			}
		}
	}
	if rawValue, exists := raw.Capabilities["tools"]; exists {
		var value bool
		if json.Unmarshal(rawValue, &value) == nil {
			candidate.Tools = model.Bool(value)
		}
	}
	addModalityCapabilities(&candidate)
	candidate.Prices, candidate.PriceErrors = parsePricing(raw.Pricings)
	if strings.HasPrefix(strings.ToLower(raw.ID), "stealth/") {
		candidate.Evidence = []model.Evidence{{Class: model.ClassStealth, Kind: "official_namespace", Field: "id", Value: "stealth/"}}
	}
	return candidate, nil
}

func parsePricing(pricings map[string]json.RawMessage) ([]model.Price, []model.PriceError) {
	var prices []model.Price
	var priceErrors []model.PriceError
	seen := make(map[string]bool)
	for _, dimension := range sortedKeys(pricings) {
		var tiers []rawPrice
		if err := source.DecodeJSON(pricings[dimension], &tiers); err != nil || len(tiers) == 0 {
			priceErrors = append(priceErrors, model.PriceError{Dimension: normalizeDimension(dimension), Code: "malformed_price"})
			continue
		}
		for _, tier := range tiers {
			decimal, err := model.ParseDecimal(tier.Value.String())
			if err != nil || decimal.Sign() < 0 || tier.Unit == "" || tier.Currency == "" {
				priceErrors = append(priceErrors, model.PriceError{Dimension: normalizeDimension(dimension), Code: "malformed_price"})
				continue
			}
			condition, err := source.CanonicalJSON(tier.Conditions)
			if err != nil {
				priceErrors = append(priceErrors, model.PriceError{Dimension: normalizeDimension(dimension), Code: "malformed_tier"})
				continue
			}
			price := model.Price{
				Dimension: normalizeDimension(dimension),
				Value:     tier.Value.String(),
				Unit:      tier.Unit,
				Currency:  strings.ToUpper(tier.Currency),
				Tier:      condition,
			}
			key := price.Dimension + "\x00" + price.Value + "\x00" + price.Unit + "\x00" + price.Currency + "\x00" + price.Tier
			if !seen[key] {
				seen[key] = true
				prices = append(prices, price)
			}
		}
	}
	return prices, priceErrors
}

func modelTimestamp(raw rawModel) time.Time {
	for _, value := range []json.RawMessage{raw.PublishTime, raw.Created} {
		if timestamp, err := flexibleTime(value); err == nil {
			return timestamp
		}
	}
	if raw.CreatedAt != "" {
		if timestamp, err := time.Parse("2006-01-02", raw.CreatedAt); err == nil {
			return timestamp
		}
	}
	return time.Time{}
}

func flexibleTime(raw json.RawMessage) (time.Time, error) {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		if timestamp, err := time.Parse("2006-01-02", text); err == nil {
			return timestamp, nil
		}
		return time.Time{}, fmt.Errorf("invalid date")
	}
	var value json.Number
	if err := source.DecodeJSON(raw, &value); err != nil {
		return time.Time{}, fmt.Errorf("invalid timestamp")
	}
	integer, err := strconv.ParseInt(value.String(), 10, 64)
	if err != nil || integer <= 0 {
		return time.Time{}, fmt.Errorf("invalid timestamp")
	}
	return time.Unix(integer, 0).UTC(), nil
}

func positiveInt(value json.Number) (int64, error) {
	integer, err := strconv.ParseInt(value.String(), 10, 64)
	if err != nil || integer <= 0 {
		return 0, fmt.Errorf("invalid positive integer")
	}
	return integer, nil
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
