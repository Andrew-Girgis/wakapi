package config

import (
	_ "embed"
	"os"

	"gopkg.in/yaml.v3"
)

//go:embed ai_prices.yml
var defaultAIPrices []byte

// AIPrice is the list price of one AI model family and set of versions, in USD per 1M tokens.
type AIPrice struct {
	Family      string   `yaml:"family"`
	Versions    []string `yaml:"versions"`
	Input       float64  `yaml:"input"`
	CachedInput float64  `yaml:"cached_input"`
	Output      float64  `yaml:"output"`
}

type aiPriceList struct {
	Models []AIPrice `yaml:"models"`
}

// LoadAIPrices reads the price list from path, or the built-in list when path is empty.
// Prices are never fetched from the network.
func LoadAIPrices(path string) ([]AIPrice, error) {
	data := defaultAIPrices
	if path != "" {
		var err error
		if data, err = os.ReadFile(path); err != nil {
			return nil, err
		}
	}
	var list aiPriceList
	if err := yaml.Unmarshal(data, &list); err != nil {
		return nil, err
	}
	return list.Models, nil
}
