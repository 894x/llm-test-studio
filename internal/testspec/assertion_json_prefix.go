package testspec

import (
	"encoding/json"
	"errors"
)

func jsonPrefixExpectation(config any) (string, any, error) {
	object, ok := config.(map[string]any)
	if !ok || len(object) != 2 {
		return "", nil, errors.New("json_prefix_equals requires exactly prefix and expected")
	}
	prefix, validPrefix := object["prefix"].(string)
	expected, hasExpected := object["expected"]
	if !validPrefix || !hasExpected {
		return "", nil, errors.New("json_prefix_equals requires a prefix string and expected value")
	}
	return prefix, expected, nil
}

func decodePrefixedJSON(actual any, prefix string) (any, bool) {
	text, ok := actual.(string)
	if !ok || len(text) > MaxDocumentBytes {
		return nil, false
	}
	if len(prefix) > MaxDocumentBytes-len(text) {
		return nil, false
	}
	value, err := decodeValue(json.RawMessage(prefix + text))
	return value, err == nil
}
