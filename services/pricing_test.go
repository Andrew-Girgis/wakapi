package services

import (
	"testing"

	"github.com/muety/wakapi/config"
	"github.com/stretchr/testify/assert"
)

func TestLoadAIPrices_BuiltIn(t *testing.T) {
	prices, err := config.LoadAIPrices("")
	assert.Nil(t, err)
	assert.NotEmpty(t, prices)

	sut := NewPricingServiceWith(prices)
	p, ok := sut.Lookup("Opus", "5-5")
	assert.True(t, ok)
	assert.Equal(t, 4.0, p.Input)
	assert.Equal(t, 0.2, p.CachedInput)
	assert.Equal(t, 20.0, p.Output)

	_, ok = sut.Lookup("Gpt", "6-sol")
	assert.True(t, ok)
	_, ok = sut.Lookup("Haiku", "4-5-20251001")
	assert.True(t, ok)
}

func TestPricingService_Cost(t *testing.T) {
	sut := NewPricingServiceWith([]config.AIPrice{{Family: "opus", Versions: []string{"5-5"}, Input: 4, CachedInput: 0.2, Output: 20}})

	cost, ok := sut.Cost("Opus", "5-5", 1_000_000, 10_000_000, 100_000)
	assert.True(t, ok)
	assert.InDelta(t, 4+2+2, cost, 1e-9)

	cost, ok = sut.Cost("Grok", "4.6", 1000, 0, 0)
	assert.False(t, ok)
	assert.Equal(t, 0.0, cost)

	_, ok = sut.Cost("Opus", "4-1", 1, 1, 1) // known family, unknown version: no price, no guess
	assert.False(t, ok)
}
