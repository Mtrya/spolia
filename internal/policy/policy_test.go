package policy

import (
	"testing"
	"time"

	"github.com/Mtrya/llmloot/internal/config"
	"github.com/Mtrya/llmloot/internal/model"
)

var policyNow = time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)

func TestDefaultPolicySelectsOnlyFreeStealthModels(t *testing.T) {
	t.Parallel()
	stealth := freeCandidate("stealth/model")
	stealth.Evidence = []model.Evidence{{Class: model.ClassStealth, Kind: "official_namespace", Field: "id"}}
	ordinaryFree := freeCandidate("ordinary/free")
	discounted := paidCandidate("discounted/model")
	discounted.Evidence = []model.Evidence{{Class: model.ClassDiscounted, Kind: "source_discount", Field: "discount"}}

	decisions := Evaluate([]model.Candidate{ordinaryFree, discounted, stealth}, Options{MinContext: 131072, Now: policyNow})
	selected := eligible(decisions)
	if len(selected) != 1 || selected[0].Candidate.ID != stealth.ID || selected[0].Class != model.ClassStealth {
		t.Fatalf("selected = %#v", selected)
	}
	if !hasReason(decisionFor(decisions, ordinaryFree.ID), "class_disabled") {
		t.Fatal("ordinary free model was not explained as disabled")
	}
	if !hasReason(decisionFor(decisions, discounted.ID), "class_disabled") {
		t.Fatal("discounted model was not explained as disabled")
	}
}

func TestFreeAndDiscountedAreIndependentAdditiveOptIns(t *testing.T) {
	t.Parallel()
	stealth := freeCandidate("stealth/model")
	stealth.Evidence = []model.Evidence{{Class: model.ClassStealth, Kind: "official_description", Field: "description"}}
	ordinaryFree := freeCandidate("ordinary/free")
	discounted := paidCandidate("discounted/model")
	discounted.Evidence = []model.Evidence{{Class: model.ClassDiscounted, Kind: "source_discount", Field: "discount"}}

	freeOnly := Evaluate([]model.Candidate{discounted, ordinaryFree, stealth}, Options{Policy: config.Policy{IncludeFree: true}, MinContext: 131072, Now: policyNow})
	selected := eligible(freeOnly)
	if len(selected) != 2 || selected[0].Class != model.ClassStealth || selected[1].Class != model.ClassFree {
		t.Fatalf("free-only selection = %#v", selected)
	}

	discountOnly := Evaluate([]model.Candidate{ordinaryFree, discounted, stealth}, Options{
		Policy: config.Policy{
			IncludeDiscounted: true,
			PriceCeilings: map[string]string{
				"prompt|per_token|USD":     "1",
				"completion|per_token|USD": "2",
				"request|per_request|USD":  "0.5",
			},
		},
		MinContext: 131072,
		Now:        policyNow,
	})
	selected = eligible(discountOnly)
	if len(selected) != 2 || selected[0].Class != model.ClassStealth || selected[1].Class != model.ClassDiscounted {
		t.Fatalf("discount-only selection = %#v", selected)
	}
}

func TestEveryNonzeroDiscountedPriceNeedsACeiling(t *testing.T) {
	t.Parallel()
	candidate := paidCandidate("discounted/model")
	candidate.Evidence = []model.Evidence{{Class: model.ClassDiscounted, Kind: "source_discount", Field: "discount"}}
	decisions := Evaluate([]model.Candidate{candidate}, Options{
		Policy: config.Policy{
			IncludeDiscounted: true,
			PriceCeilings: map[string]string{
				"prompt|per_token|USD":     "1",
				"completion|per_token|USD": "2",
			},
		},
		MinContext: 131072,
		Now:        policyNow,
	})
	if decisions[0].Eligible || !hasReason(decisions[0], "missing_price_ceiling") {
		t.Fatalf("decision = %#v", decisions[0])
	}

	ceilings := map[string]string{
		"prompt|per_token|USD":     "0.9999999999999999999",
		"completion|per_token|USD": "2",
		"request|per_request|USD":  "0.5",
	}
	decisions = Evaluate([]model.Candidate{candidate}, Options{Policy: config.Policy{IncludeDiscounted: true, PriceCeilings: ceilings}, MinContext: 131072, Now: policyNow})
	if decisions[0].Eligible || !hasReason(decisions[0], "price_above_ceiling") {
		t.Fatalf("exact decimal ceiling decision = %#v", decisions[0])
	}
}

func TestFreePricingAllowsMissingOptionalDimensionsButRejectsAnyPublishedNonzeroTier(t *testing.T) {
	t.Parallel()
	missingOptional := freeCandidate("free/without-request-price")
	withNonzeroTier := freeCandidate("free/nonzero-tier")
	withNonzeroTier.Prices = append(withNonzeroTier.Prices, model.Price{Dimension: "input_cache_read", Value: "1e-18", Unit: "per_token", Currency: "USD", Tier: `{"min_prompt_tokens":1}`})

	decisions := Evaluate([]model.Candidate{withNonzeroTier, missingOptional}, Options{Policy: config.Policy{IncludeFree: true}, MinContext: 131072, Now: policyNow})
	if !decisionFor(decisions, missingOptional.ID).Eligible {
		t.Fatal("free model with omitted optional price was excluded")
	}
	if decisionFor(decisions, withNonzeroTier.ID).Eligible {
		t.Fatal("free model with a nonzero published tier was selected")
	}
}

func TestCompatibilityUsesExplicitFacts(t *testing.T) {
	t.Parallel()
	unknownTools := freeCandidate("tools/unknown")
	unknownTools.Tools = nil
	explicitlyUnsupported := freeCandidate("tools/unsupported")
	explicitlyUnsupported.Tools = model.Bool(false)
	router := freeCandidate("router/model")
	router.Kind = model.KindRouter

	decisions := Evaluate([]model.Candidate{explicitlyUnsupported, router, unknownTools}, Options{Policy: config.Policy{IncludeFree: true}, MinContext: 131072, Now: policyNow})
	if !decisionFor(decisions, unknownTools.ID).Eligible {
		t.Fatal("unknown tool support was rejected")
	}
	if !hasReason(decisionFor(decisions, explicitlyUnsupported.ID), "tools_explicitly_unsupported") {
		t.Fatal("explicit tool disablement was ignored")
	}
	if !hasReason(decisionFor(decisions, router.ID), "not_concrete_model") {
		t.Fatal("router was treated as a concrete model")
	}
}

func TestRankingUsesClassThenCreationFirstSeenAndID(t *testing.T) {
	t.Parallel()
	olderStealth := freeCandidate("z-stealth")
	olderStealth.CreatedAt = policyNow.Add(-48 * time.Hour)
	olderStealth.Evidence = []model.Evidence{{Class: model.ClassStealth, Kind: "official_description", Field: "description"}}
	newer := freeCandidate("newer")
	newer.CreatedAt = policyNow.Add(-time.Hour)
	firstSeenNewer := freeCandidate("b-first-seen")
	firstSeenNewer.CreatedAt = policyNow.Add(-24 * time.Hour)
	firstSeenNewer.FirstSeen = policyNow.Add(-time.Hour)
	firstSeenOlder := freeCandidate("a-first-seen")
	firstSeenOlder.CreatedAt = firstSeenNewer.CreatedAt
	firstSeenOlder.FirstSeen = policyNow.Add(-2 * time.Hour)

	decisions := eligible(Evaluate([]model.Candidate{firstSeenOlder, newer, olderStealth, firstSeenNewer}, Options{Policy: config.Policy{IncludeFree: true}, MinContext: 131072, Now: policyNow}))
	want := []string{"z-stealth", "newer", "b-first-seen", "a-first-seen"}
	for index, id := range want {
		if decisions[index].Candidate.ID != id {
			t.Fatalf("rank %d = %q, want %q", index, decisions[index].Candidate.ID, id)
		}
	}
}

func freeCandidate(id string) model.Candidate {
	return model.Candidate{
		Source:           "fixture",
		ID:               id,
		DisplayName:      id,
		Kind:             model.KindModel,
		Protocol:         "openai_chat",
		CreatedAt:        policyNow.Add(-24 * time.Hour),
		ContextWindow:    200000,
		OutputModalities: []string{"text"},
		Tools:            model.Bool(true),
		Prices: []model.Price{
			{Dimension: "prompt", Value: "0", Unit: "per_token", Currency: "USD"},
			{Dimension: "completion", Value: "0", Unit: "per_token", Currency: "USD"},
		},
	}
}

func paidCandidate(id string) model.Candidate {
	candidate := freeCandidate(id)
	candidate.Prices = []model.Price{
		{Dimension: "prompt", Value: "1", Unit: "per_token", Currency: "USD"},
		{Dimension: "completion", Value: "2", Unit: "per_token", Currency: "USD"},
		{Dimension: "request", Value: "0.5", Unit: "per_request", Currency: "USD"},
	}
	return candidate
}

func eligible(decisions []Decision) []Decision {
	var result []Decision
	for _, decision := range decisions {
		if decision.Eligible {
			result = append(result, decision)
		}
	}
	return result
}

func decisionFor(decisions []Decision, id string) Decision {
	for _, decision := range decisions {
		if decision.Candidate.ID == id {
			return decision
		}
	}
	return Decision{}
}

func hasReason(decision Decision, code string) bool {
	for _, reason := range decision.Reasons {
		if reason.Code == code {
			return true
		}
	}
	return false
}
