package policy

import (
	"sort"
	"strings"
	"time"

	"github.com/Mtrya/llmloot/internal/config"
	"github.com/Mtrya/llmloot/internal/model"
)

type Options struct {
	Policy     config.Policy
	MinContext int64
	Now        time.Time
}

type Reason struct {
	Code  string `json:"code"`
	Field string `json:"field,omitempty"`
}

type Decision struct {
	Candidate model.Candidate        `json:"candidate"`
	Eligible  bool                   `json:"eligible"`
	Class     model.EligibilityClass `json:"class,omitempty"`
	Reasons   []Reason               `json:"reasons,omitempty"`
}

func Evaluate(candidates []model.Candidate, options Options) []Decision {
	decisions := make([]Decision, 0, len(candidates))
	for _, candidate := range candidates {
		decisions = append(decisions, classify(candidate, options))
	}
	sort.SliceStable(decisions, func(left, right int) bool {
		leftDecision, rightDecision := decisions[left], decisions[right]
		if leftDecision.Eligible != rightDecision.Eligible {
			return leftDecision.Eligible
		}
		if leftDecision.Eligible {
			if classRank(leftDecision.Class) != classRank(rightDecision.Class) {
				return classRank(leftDecision.Class) < classRank(rightDecision.Class)
			}
			if !leftDecision.Candidate.CreatedAt.Equal(rightDecision.Candidate.CreatedAt) {
				return leftDecision.Candidate.CreatedAt.After(rightDecision.Candidate.CreatedAt)
			}
			if !leftDecision.Candidate.FirstSeen.Equal(rightDecision.Candidate.FirstSeen) {
				return leftDecision.Candidate.FirstSeen.After(rightDecision.Candidate.FirstSeen)
			}
		}
		return leftDecision.Candidate.ID < rightDecision.Candidate.ID
	})
	return decisions
}

func classify(candidate model.Candidate, options Options) Decision {
	decision := Decision{Candidate: candidate}
	decision.Reasons = compatibilityReasons(candidate, options)
	if len(decision.Reasons) > 0 {
		return decision
	}

	prices := inspectPrices(candidate)
	hasStealthEvidence := hasEvidence(candidate, model.ClassStealth)
	hasDiscountEvidence := hasEvidence(candidate, model.ClassDiscounted)

	if prices.valid && prices.free && hasStealthEvidence {
		decision.Eligible = true
		decision.Class = model.ClassStealth
		return decision
	}
	if prices.valid && prices.free && options.Policy.IncludeFree {
		decision.Eligible = true
		decision.Class = model.ClassFree
		return decision
	}
	if prices.valid && !prices.free && prices.nonzero && hasDiscountEvidence && options.Policy.IncludeDiscounted {
		if reasons := ceilingReasons(candidate.Prices, options.Policy.PriceCeilings); len(reasons) == 0 {
			decision.Eligible = true
			decision.Class = model.ClassDiscounted
			return decision
		} else {
			decision.Reasons = reasons
			return decision
		}
	}

	if !prices.valid {
		decision.Reasons = prices.reasons
		return decision
	}
	if prices.free {
		if hasStealthEvidence {
			decision.Reasons = []Reason{{Code: "stealth_price_invalid", Field: "pricing"}}
		} else {
			decision.Reasons = []Reason{{Code: "class_disabled", Field: "free"}}
		}
		return decision
	}
	if hasStealthEvidence && !hasDiscountEvidence {
		decision.Reasons = append(decision.Reasons, Reason{Code: "stealth_requires_free", Field: "pricing"})
	}
	if hasDiscountEvidence {
		decision.Reasons = append(decision.Reasons, Reason{Code: "class_disabled", Field: "discounted"})
	} else {
		decision.Reasons = append(decision.Reasons, Reason{Code: "no_discount_evidence", Field: "evidence"})
	}
	return decision
}

func compatibilityReasons(candidate model.Candidate, options Options) []Reason {
	var reasons []Reason
	if candidate.Kind != model.KindModel {
		reasons = append(reasons, Reason{Code: "not_concrete_model", Field: "kind"})
	}
	if candidate.Protocol != "openai_chat" {
		reasons = append(reasons, Reason{Code: "unsupported_protocol", Field: "protocol"})
	}
	if !contains(candidate.OutputModalities, "text") {
		reasons = append(reasons, Reason{Code: "no_text_output", Field: "output_modalities"})
	}
	if candidate.ContextWindow <= 0 {
		reasons = append(reasons, Reason{Code: "invalid_context", Field: "context_window"})
	} else if candidate.ContextWindow < options.MinContext {
		reasons = append(reasons, Reason{Code: "context_below_minimum", Field: "context_window"})
	}
	if candidate.Tools != nil && !*candidate.Tools {
		reasons = append(reasons, Reason{Code: "tools_explicitly_unsupported", Field: "tools"})
	}
	if candidate.ExpiresAt != nil && !options.Now.IsZero() && !candidate.ExpiresAt.After(options.Now) {
		reasons = append(reasons, Reason{Code: "expired", Field: "expires_at"})
	}
	return reasons
}

type priceInspection struct {
	valid   bool
	free    bool
	nonzero bool
	reasons []Reason
}

func inspectPrices(candidate model.Candidate) priceInspection {
	result := priceInspection{valid: true, free: true}
	for _, problem := range candidate.PriceErrors {
		result.reasons = append(result.reasons, Reason{Code: problem.Code, Field: problem.Dimension})
	}
	if len(candidate.PriceErrors) > 0 {
		result.valid = false
		result.free = false
	}

	required := map[string]bool{"prompt": false, "completion": false}
	for _, price := range candidate.Prices {
		decimal, err := model.ParseDecimal(price.Value)
		if err != nil || decimal.Sign() < 0 {
			result.valid = false
			result.free = false
			result.reasons = append(result.reasons, Reason{Code: "malformed_price", Field: price.Dimension})
			continue
		}
		if _, exists := required[price.Dimension]; exists {
			required[price.Dimension] = true
			if price.Currency != "USD" || !isTokenUnit(price.Unit) {
				result.valid = false
				result.free = false
				result.reasons = append(result.reasons, Reason{Code: "unsupported_required_price_unit", Field: price.Dimension})
			}
		}
		if decimal.Sign() != 0 {
			result.free = false
			result.nonzero = true
		}
	}
	for dimension, present := range required {
		if !present {
			result.valid = false
			result.free = false
			result.reasons = append(result.reasons, Reason{Code: "missing_required_price", Field: dimension})
		}
	}
	result.reasons = uniqueReasons(result.reasons)
	return result
}

func ceilingReasons(prices []model.Price, ceilings map[string]string) []Reason {
	var reasons []Reason
	for _, price := range prices {
		value, err := model.ParseDecimal(price.Value)
		if err != nil || value.Sign() < 0 {
			reasons = append(reasons, Reason{Code: "malformed_price", Field: price.Dimension})
			continue
		}
		if value.Sign() == 0 {
			continue
		}
		ceilingText, exists := ceilings[price.Key()]
		if !exists {
			reasons = append(reasons, Reason{Code: "missing_price_ceiling", Field: price.Key()})
			continue
		}
		ceiling, err := model.ParseDecimal(ceilingText)
		if err != nil || ceiling.Sign() < 0 {
			reasons = append(reasons, Reason{Code: "invalid_price_ceiling", Field: price.Key()})
			continue
		}
		if value.Cmp(ceiling) > 0 {
			reasons = append(reasons, Reason{Code: "price_above_ceiling", Field: price.Key()})
		}
	}
	return uniqueReasons(reasons)
}

func hasEvidence(candidate model.Candidate, class model.EligibilityClass) bool {
	for _, evidence := range candidate.Evidence {
		if evidence.Class == class {
			return true
		}
	}
	return false
}

func classRank(class model.EligibilityClass) int {
	switch class {
	case model.ClassStealth:
		return 0
	case model.ClassFree:
		return 1
	case model.ClassDiscounted:
		return 2
	default:
		return 3
	}
}

func isTokenUnit(unit string) bool {
	switch unit {
	case "per_token", "perMTokens":
		return true
	default:
		return false
	}
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if strings.EqualFold(value, wanted) {
			return true
		}
	}
	return false
}

func uniqueReasons(reasons []Reason) []Reason {
	seen := make(map[string]bool)
	result := make([]Reason, 0, len(reasons))
	for _, reason := range reasons {
		key := reason.Code + "\x00" + reason.Field
		if !seen[key] {
			seen[key] = true
			result = append(result, reason)
		}
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].Code != result[right].Code {
			return result[left].Code < result[right].Code
		}
		return result[left].Field < result[right].Field
	})
	return result
}
