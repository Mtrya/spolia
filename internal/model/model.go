package model

import "time"

type Kind string

const (
	KindModel   Kind = "model"
	KindAlias   Kind = "alias"
	KindRouter  Kind = "router"
	KindUtility Kind = "utility"
)

type EligibilityClass string

const (
	ClassStealth EligibilityClass = "stealth"
	ClassFree    EligibilityClass = "free"
)

type Price struct {
	Dimension string `json:"dimension"`
	Value     string `json:"value"`
	Unit      string `json:"unit"`
	Currency  string `json:"currency"`
	Tier      string `json:"tier,omitempty"`
}

func (p Price) Key() string {
	return PriceKey(p.Dimension, p.Unit, p.Currency)
}

func PriceKey(dimension, unit, currency string) string {
	return dimension + "|" + unit + "|" + currency
}

type PriceError struct {
	Dimension string `json:"dimension"`
	Code      string `json:"code"`
}

type Evidence struct {
	Class EligibilityClass `json:"class"`
	Kind  string           `json:"kind"`
	Field string           `json:"field"`
	Value string           `json:"value,omitempty"`
}

type Candidate struct {
	Source           string          `json:"source"`
	ID               string          `json:"id"`
	DisplayName      string          `json:"display_name"`
	Kind             Kind            `json:"kind"`
	Protocol         string          `json:"protocol"`
	CreatedAt        time.Time       `json:"created_at,omitempty"`
	FirstSeen        time.Time       `json:"first_seen,omitempty"`
	ExpiresAt        *time.Time      `json:"expires_at,omitempty"`
	ContextWindow    int64           `json:"context_window"`
	MaxOutput        *int64          `json:"max_output,omitempty"`
	InputModalities  []string        `json:"input_modalities,omitempty"`
	OutputModalities []string        `json:"output_modalities,omitempty"`
	Tools            *bool           `json:"tools,omitempty"`
	Capabilities     map[string]bool `json:"capabilities,omitempty"`
	Prices           []Price         `json:"prices"`
	PriceErrors      []PriceError    `json:"price_errors,omitempty"`
	Evidence         []Evidence      `json:"evidence,omitempty"`
}

func Bool(value bool) *bool {
	return &value
}

func Int64(value int64) *int64 {
	return &value
}
