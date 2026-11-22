package server

import (
	"bytes"
	"encoding/json"
	"io"

	"github.com/moni-ai/moni-aigate/internal/routing"
	"github.com/moni-ai/moni-aigate/internal/strictjson"
)

type routePreferenceWire struct {
	Order             []string          `json:"order"`
	Only              []string          `json:"only"`
	Ignore            []string          `json:"ignore"`
	AllowFallbacks    *bool             `json:"allow_fallbacks"`
	RequireParameters bool              `json:"require_parameters"`
	Sort              string            `json:"sort"`
	MaxPrice          *routing.MaxPrice `json:"max_price"`
	DataCollection    string            `json:"data_collection"`
}

func parseChatRoute(raw []byte) ([]byte, routing.RequestShape, error) {
	shape := routing.RequestShape{Preference: routing.RoutePreference{AllowFallbacks: true}, StatsWindowS: 900, StatsMinSamples: 50}
	value, err := strictjson.Parse(raw, 1<<20)
	if err != nil {
		return nil, shape, err
	}
	root, ok := value.(map[string]any)
	if !ok {
		return nil, shape, routing.ErrInvalidPolicy
	}
	routeValue, exists := root["route"]
	if !exists {
		return raw, shape, nil
	}
	routeObject, ok := routeValue.(map[string]any)
	if !ok || routeObject == nil {
		return nil, shape, routing.ErrInvalidPolicy
	}
	for key, value := range routeObject {
		if value == nil {
			return nil, shape, routing.ErrInvalidPolicy
		}
		if key == "order" || key == "only" || key == "ignore" {
			values, ok := routeObject[key].([]any)
			if !ok || len(values) == 0 {
				return nil, shape, routing.ErrInvalidPolicy
			}
		}
		if key == "max_price" {
			prices, ok := routeObject[key].(map[string]any)
			if !ok || len(prices) == 0 {
				return nil, shape, routing.ErrInvalidPolicy
			}
			for _, price := range prices {
				if price == nil {
					return nil, shape, routing.ErrInvalidPolicy
				}
			}
		}
	}
	encoded, err := json.Marshal(routeObject)
	if err != nil {
		return nil, shape, err
	}
	var wire routePreferenceWire
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&wire) != nil || decoder.Decode(new(any)) != io.EOF {
		return nil, shape, routing.ErrInvalidPolicy
	}
	shape.Preference = routing.RoutePreference{Order: wire.Order, Only: wire.Only, Ignore: wire.Ignore, AllowFallbacks: true, RequireParameters: wire.RequireParameters, Sort: wire.Sort, MaxPrice: wire.MaxPrice, DataCollection: wire.DataCollection}
	if wire.AllowFallbacks != nil {
		shape.Preference.AllowFallbacks = *wire.AllowFallbacks
	}
	if _, exists := routeObject["sort"]; exists && wire.Sort == "" {
		return nil, shape, routing.ErrInvalidPolicy
	}
	if _, exists := routeObject["data_collection"]; exists && wire.DataCollection == "" {
		return nil, shape, routing.ErrInvalidPolicy
	}
	if err := shape.Validate(); err != nil {
		return nil, shape, err
	}
	delete(root, "route")
	stripped, err := json.Marshal(root)
	return stripped, shape, err
}

func validateRouteSelectors(candidates []routing.Candidate, preference routing.RoutePreference) error {
	for _, selectors := range [][]string{preference.Order, preference.Only, preference.Ignore} {
		for _, selector := range selectors {
			found := false
			for _, candidate := range candidates {
				if selector == candidate.ChannelID || selector == candidate.PhysicalModelID || selector == candidate.Model {
					found = true
					break
				}
			}
			if !found {
				return routing.ErrInvalidPolicy
			}
		}
	}
	if len(preference.Only) > 0 {
		matches := 0
		seen := map[string]bool{}
		for _, candidate := range candidates {
			if seen[candidate.BindingID] {
				continue
			}
			for _, selector := range preference.Only {
				if selector == candidate.ChannelID || selector == candidate.PhysicalModelID || selector == candidate.Model {
					seen[candidate.BindingID] = true
					matches++
					break
				}
			}
		}
		if matches == 0 {
			return routing.ErrInvalidPolicy
		}
	}
	return nil
}
