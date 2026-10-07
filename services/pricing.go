package services

import (
	"log/slog"
	"strings"

	"github.com/muety/wakapi/config"
)

// PricingService estimates the API-equivalent cost of AI tokens from a local price list.
type PricingService struct {
	prices []config.AIPrice
}

func NewPricingService() *PricingService {
	cfg := config.Get()
	prices, err := config.LoadAIPrices(cfg.App.AIPricesFile)
	if err != nil {
		slog.Error("failed to load ai price list, cost estimates are disabled", "file", cfg.App.AIPricesFile, "error", err)
	}
	return &PricingService{prices: prices}
}

func NewPricingServiceWith(prices []config.AIPrice) *PricingService {
	return &PricingService{prices: prices}
}

// Lookup returns the price for a model family (e.g. "Opus") and version (e.g. "5-5").
func (srv *PricingService) Lookup(model, version string) (config.AIPrice, bool) {
	for _, p := range srv.prices {
		if !strings.EqualFold(p.Family, model) {
			continue
		}
		for _, v := range p.Versions {
			if strings.EqualFold(v, version) {
				return p, true
			}
		}
	}
	return config.AIPrice{}, false
}

// Cost returns the estimated cost in USD, and false if the model has no price.
func (srv *PricingService) Cost(model, version string, input, cachedInput, output int64) (float64, bool) {
	p, ok := srv.Lookup(model, version)
	if !ok {
		return 0, false
	}
	return (float64(input)*p.Input + float64(cachedInput)*p.CachedInput + float64(output)*p.Output) / 1e6, true
}
