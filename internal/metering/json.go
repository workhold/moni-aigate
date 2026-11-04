package metering

import (
	"encoding/json"
	"errors"
	"strconv"

	"github.com/moni-ai/moni-aigate/internal/strictjson"
)

func parseJSON(raw []byte, maximum int) (any, error) {
	value, err := strictjson.Parse(raw, maximum)
	if errors.Is(err, strictjson.ErrTooLarge) {
		return nil, ErrTooLarge
	}
	if err != nil {
		return nil, ErrInvalidJSON
	}
	return value, nil
}

func exactObject(value any, fields ...string) (map[string]any, bool) {
	object, ok := value.(map[string]any)
	if !ok || len(object) != len(fields) {
		return nil, false
	}
	for _, field := range fields {
		if _, exists := object[field]; !exists {
			return nil, false
		}
	}
	return object, true
}

func stringValue(value any) (string, bool) { s, ok := value.(string); return s, ok }

func decimal(value any) (int64, bool) {
	s, ok := stringValue(value)
	if !ok || len(s) == 0 || len(s) > 19 || len(s) > 1 && s[0] == '0' {
		return 0, false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	return n, err == nil
}

func validUUID(id string) bool {
	if len(id) != 36 || id == "00000000-0000-0000-0000-000000000000" {
		return false
	}
	for i := range id {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if id[i] != '-' {
				return false
			}
		} else if !(id[i] >= '0' && id[i] <= '9' || id[i] >= 'a' && id[i] <= 'f') {
			return false
		}
	}
	return true
}

func sortedStrings(value any) ([]string, bool) {
	array, ok := value.([]any)
	if !ok || len(array) == 0 || len(array) > 8 {
		return nil, false
	}
	result := make([]string, len(array))
	for i, value := range array {
		s, ok := stringValue(value)
		if !ok || registryUnit(s) == "" || i > 0 && s <= result[i-1] {
			return nil, false
		}
		result[i] = s
	}
	return result, true
}

func canonical(value any) []byte {
	// Only fixed structs of validated ASCII strings reach this function.
	raw, _ := json.Marshal(value)
	return raw
}
