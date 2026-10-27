package services

import (
	"encoding/json"
	"github.com/muety/wakapi/config"
	"github.com/muety/wakapi/models"
	"math"
)

func positiveRate(n float64) bool { return n > 0 && !math.IsNaN(n) && !math.IsInf(n, 0) }
func optionalRate(p *float64) bool {
	return p != nil && *p >= 0 && !math.IsNaN(*p) && !math.IsInf(*p, 0)
}
func sameRate(a, b *float64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// Exact configured versions and explicitly recognized punctuation/family aliases.
// Never guess latest models, CLI versions, mini/fast/codex variants, or date suffixes.
// Conflicting duplicate rate definitions fail closed rather than choosing a winner.
func (s *PricingService) LookupLedger(model string) (config.AIPrice, bool) {
	if model == "" {
		return config.AIPrice{}, false
	}
	var found config.AIPrice
	matched := false
	for _, p := range s.prices {
		hit := false
		for _, v := range p.Versions {
			if metricModel(p.Family+" "+v) == metricModel(model) {
				hit = true
				break
			}
		}
		if !hit {
			continue
		}
		if !positiveRate(p.Input) || !positiveRate(p.CachedInput) || !positiveRate(p.Output) {
			return config.AIPrice{}, false
		}
		if matched && (found.Input != p.Input || found.CachedInput != p.CachedInput || found.Output != p.Output || !sameRate(found.CacheWrite5m, p.CacheWrite5m) || !sameRate(found.CacheWrite1h, p.CacheWrite1h)) {
			return config.AIPrice{}, false
		}
		found = p
		matched = true
	}
	return found, matched
}

// Only explicit, integer, complete TTL splits can price cache-write tokens.
// Aggregate write counts alone do not prove 5m vs 1h caching. Unknown input is
// always excluded; reasoning is metadata already within output, never extra cost.
func (s *PricingService) LedgerEventCost(e models.TokenUsageEvent) (float64, int64) {
	p, ok := s.LookupLedger(e.Model)
	if !ok {
		return 0, 0
	}
	value := (float64(e.InputTokens)*p.Input + float64(e.CacheReadTokens)*p.CachedInput + float64(e.OutputTokens)*p.Output) / 1e6
	priced := e.InputTokens + e.CacheReadTokens + e.OutputTokens
	if e.CacheWriteTokens > 0 {
		var raw struct {
			CacheCreation map[string]json.Number `json:"cache_creation"`
		}
		if json.Unmarshal([]byte(e.RawUsageJSON), &raw) == nil {
			five, err5 := raw.CacheCreation["ephemeral_5m_input_tokens"].Int64()
			hour, err1 := raw.CacheCreation["ephemeral_1h_input_tokens"].Int64()
			if err5 == nil && err1 == nil && five >= 0 && hour >= 0 && five <= e.CacheWriteTokens && hour == e.CacheWriteTokens-five {
				if optionalRate(p.CacheWrite5m) {
					priced += five
					value += float64(five) * *p.CacheWrite5m / 1e6
				}
				if optionalRate(p.CacheWrite1h) {
					priced += hour
					value += float64(hour) * *p.CacheWrite1h / 1e6
				}
			}
		}
	}
	return value, priced
}
