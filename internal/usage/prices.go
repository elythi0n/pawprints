package usage

import (
	"regexp"
	"strings"

	"github.com/elythi0n/pawprints/internal/store"
)

// Anthropic's API list prices in dollars per million tokens: input, output,
// and cache reads. Cache writes are priced from input, 1.25x for the
// five-minute kind and 2x for the one-hour kind. Updated with each release;
// "prices" in config.json overrides a model.
var apiPrices = map[string]store.Price{
	"claude-fable-5-1":  {Input: 10, Output: 50, CacheRead: 0.25},
	"claude-mythos-5-1": {Input: 10, Output: 50, CacheRead: 0.25},
	"claude-fable-5":    {Input: 10, Output: 50, CacheRead: 1},
	"claude-mythos-5":   {Input: 10, Output: 50, CacheRead: 1},
	"claude-opus-5-5":   {Input: 4, Output: 20, CacheRead: 0.20},
	"claude-opus-5":     {Input: 5, Output: 25, CacheRead: 0.50},
	"claude-opus-4-8":   {Input: 5, Output: 25, CacheRead: 0.50},
	"claude-opus-4-7":   {Input: 5, Output: 25, CacheRead: 0.50},
	"claude-opus-4-6":   {Input: 5, Output: 25, CacheRead: 0.50},
	"claude-opus-4-5":   {Input: 5, Output: 25, CacheRead: 0.50},
	"claude-opus-4-1":   {Input: 15, Output: 75, CacheRead: 1.50},
	"claude-opus-4":     {Input: 15, Output: 75, CacheRead: 1.50},
	"claude-sonnet-5-5": {Input: 2, Output: 10, CacheRead: 0.20},
	"claude-sonnet-5":   {Input: 2, Output: 10, CacheRead: 0.20},
	"claude-sonnet-4-6": {Input: 3, Output: 15, CacheRead: 0.30},
	"claude-sonnet-4-5": {Input: 3, Output: 15, CacheRead: 0.30},
	"claude-sonnet-4":   {Input: 3, Output: 15, CacheRead: 0.30},
	"claude-haiku-5-5":  {Input: 0.10, Output: 0.50, CacheRead: 0.01},
	"claude-haiku-4-5":  {Input: 1, Output: 5, CacheRead: 0.10},
	"claude-3-5-haiku":  {Input: 0.80, Output: 4, CacheRead: 0.08},
}

var dateSuffix = regexp.MustCompile(`[-@]\d{8}$`)

// canonicalModel turns the ids cloud providers and dated releases use
// ("us.anthropic.claude-sonnet-4-5-20250929-v1:0", "claude-opus-4-5@20251101")
// into the plain model id.
func canonicalModel(model string) string {
	m := strings.ToLower(model)
	if i := strings.Index(m, "claude-"); i > 0 {
		m = m[i:]
	}
	if i := strings.Index(m, "-v1"); i > 0 {
		m = m[:i]
	}
	m = strings.TrimSuffix(m, "[1m]")
	return dateSuffix.ReplaceAllString(m, "")
}

// PriceFor is a model's price: config.json's, else the built-in one.
func PriceFor(model string, overrides map[string]store.Price) (store.Price, bool) {
	if p, ok := overrides[model]; ok {
		return p, true
	}
	id := canonicalModel(model)
	if p, ok := overrides[id]; ok {
		return p, true
	}
	p, ok := apiPrices[id]
	return p, ok
}

// APICost is what usage would cost at API prices. ok is false when the model
// has no known price; the cost is then zero rather than a guess.
func APICost(model string, u store.Usage, overrides map[string]store.Price) (float64, bool) {
	p, ok := PriceFor(model, overrides)
	if !ok {
		return 0, false
	}
	write5m := p.CacheWrite
	if write5m == 0 {
		write5m = p.Input * 1.25
	}
	oneHour := u.CacheWrite1h
	if oneHour > u.CacheWrite {
		oneHour = u.CacheWrite
	}
	cost := float64(u.Input)*p.Input +
		float64(u.Output)*p.Output +
		float64(u.CacheRead)*p.CacheRead +
		float64(u.CacheWrite-oneHour)*write5m +
		float64(oneHour)*p.Input*2
	return cost / 1e6, true
}

// Cost is the API cost of a usage map by model. ok is false when some model
// in use has no price; the total then covers the priced models only.
func Cost(byModel map[string]store.Usage, overrides map[string]store.Price) (float64, bool) {
	total, complete := 0.0, true
	for model, u := range byModel {
		if model == "<synthetic>" || u.Counted()+u.CacheRead == 0 {
			continue
		}
		c, ok := APICost(model, u, overrides)
		if !ok {
			complete = false
			continue
		}
		total += c
	}
	return total, complete
}
