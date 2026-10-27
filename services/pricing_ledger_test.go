package services

import (
	"github.com/muety/wakapi/config"
	"github.com/muety/wakapi/models"
	"github.com/stretchr/testify/require"
	"math"
	"testing"
)

func TestLedgerPricing_ExactVersionsUnknownInputAndReasoning(t *testing.T) {
	rate := config.AIPrice{Family: "opus", Versions: []string{"4-6", "4.6"}, Input: 3, CachedInput: .3, Output: 15}
	s := NewPricingServiceWith([]config.AIPrice{rate})
	for _, name := range []string{"claude-opus-4-6", "Opus 4.6", "anthropic/claude-opus-4.6"} {
		_, ok := s.LookupLedger(name)
		require.True(t, ok)
	}
	for _, name := range []string{"", "Claude 2.1.150", "claude-opus-4-6-fast", "claude-opus-4-6-20260901", "claude-opus-5"} {
		_, ok := s.LookupLedger(name)
		require.False(t, ok)
	}
	e := models.TokenUsageEvent{Model: "claude-opus-4-6", InputTokens: 10, CacheReadTokens: 100, CacheWriteTokens: 20, UnknownInputTokens: 30, OutputTokens: 2, ReasoningTokens: 1, RawUsageJSON: `{}`}
	value, priced := s.LedgerEventCost(e)
	require.InDelta(t, .00009, value, 1e-12)
	require.Equal(t, int64(112), priced)
	e.ReasoningTokens = 200
	e.UnknownInputTokens = 999999
	again, n := s.LedgerEventCost(e)
	require.Equal(t, value, again)
	require.Equal(t, priced, n)
}

func TestLedgerPricing_CacheWritesNeedBothTTLAndExplicitRate(t *testing.T) {
	five, hour := 3.75, 6.0
	rate := config.AIPrice{Family: "opus", Versions: []string{"4-6"}, Input: 3, CachedInput: .3, Output: 15, CacheWrite5m: &five, CacheWrite1h: &hour}
	s := NewPricingServiceWith([]config.AIPrice{rate})
	e := models.TokenUsageEvent{Model: "claude-opus-4-6", CacheWriteTokens: 20, UnknownInputTokens: 100, RawUsageJSON: `{"cache_creation":{"ephemeral_5m_input_tokens":7,"ephemeral_1h_input_tokens":13}}`}
	value, n := s.LedgerEventCost(e)
	require.InDelta(t, .00010425, value, 1e-12)
	require.Equal(t, int64(20), n)
	for _, raw := range []string{`{}`, `{"cache_creation":{"ephemeral_5m_input_tokens":20}}`, `{"cache_creation":{"ephemeral_5m_input_tokens":7,"ephemeral_1h_input_tokens":14}}`, `{"cache_creation":{"ephemeral_5m_input_tokens":7.5,"ephemeral_1h_input_tokens":12.5}}`} {
		e.RawUsageJSON = raw
		v, n := s.LedgerEventCost(e)
		require.Zero(t, v)
		require.Zero(t, n)
	}
	e.RawUsageJSON = `{"cache_creation":{"ephemeral_5m_input_tokens":7,"ephemeral_1h_input_tokens":13}}`
	rate.CacheWrite1h = nil
	s = NewPricingServiceWith([]config.AIPrice{rate})
	value, n = s.LedgerEventCost(e)
	require.InDelta(t, .00002625, value, 1e-12)
	require.Equal(t, int64(7), n)
}

func TestLedgerPricing_InvalidAndAmbiguousRatesFailClosed(t *testing.T) {
	rate := config.AIPrice{Family: "gpt", Versions: []string{"5.4"}, Input: 2.5, CachedInput: .25, Output: 15}
	for _, bad := range []float64{-1, math.NaN(), math.Inf(1)} {
		p := rate
		p.Input = bad
		s := NewPricingServiceWith([]config.AIPrice{p})
		_, ok := s.LookupLedger("gpt-5.4")
		require.False(t, ok)
	}
	other := rate
	other.Output = 16
	s := NewPricingServiceWith([]config.AIPrice{rate, other})
	_, ok := s.LookupLedger("gpt-5.4")
	require.False(t, ok)
	s = NewPricingServiceWith([]config.AIPrice{rate, rate})
	_, ok = s.LookupLedger("gpt-5.4")
	require.True(t, ok)
	_, ok = s.LookupLedger("gpt-5.4-mini")
	require.False(t, ok)
}

func TestLedgerPricing_PartialTotalsReconcileAndBillingStaysUnknown(t *testing.T) {
	s := newDashboardTestService()
	defer config.Set(config.Empty())
	rows := []models.TokenUsageEvent{{Model: "claude-opus-5-5", InputTokens: 1000000, CacheReadTokens: 1000000, CacheWriteTokens: 100, UnknownInputTokens: 200, OutputTokens: 1000000, ReasoningTokens: 999999}, {Model: "unknown-model", InputTokens: 300}}
	c := s.ledgerCost(rows)
	require.True(t, c.Available)
	require.Equal(t, 24.2, c.EstimatedAPIValue)
	require.Equal(t, int64(3000000), c.PricedTokens)
	require.Equal(t, int64(600), c.UnpricedTokens)
	require.Equal(t, ledgerTotals(rows).Total, c.PricedTokens+c.UnpricedTokens)
	require.False(t, c.BillingAttributionKnown)
	require.Zero(t, c.SubscriptionValue)
	require.Zero(t, c.PayPerTokenValue)
	var accounted int64
	for _, m := range c.ByModel {
		require.Equal(t, m.Tokens.Total, m.PricedTokens+m.UnpricedTokens)
		accounted += m.Tokens.Total
	}
	require.Equal(t, ledgerTotals(rows).Total, accounted)
}
