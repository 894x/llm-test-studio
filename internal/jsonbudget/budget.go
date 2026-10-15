// Package jsonbudget bounds current owned DTO collections before typed decoding.
package jsonbudget

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

const (
	MaxArrayItems = 131_072
	MaxDepth      = 128
)

type scanMode uint8

const (
	schemaFields scanMode = iota
	rawJSONValues
	caseInputCases
	ownedMapKeys
)

var (
	ErrInvalid  = errors.New("invalid owned JSON document")
	ErrExceeded = errors.New("owned JSON exceeds collection budget")
)

// Check preserves native RawMessage leaves while counting current owned DTOs.
func Check(raw []byte, limit int) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	remaining := limit
	if err := scanDocument(
		decoder,
		&remaining,
		0,
		schemaFields,
	); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return ErrInvalid
	}
	return nil
}

func scanDocument(decoder *json.Decoder, remaining *int, depth int, mode scanMode) error {
	if depth > MaxDepth {
		return ErrExceeded
	}
	token, err := decoder.Token()
	if err != nil {
		return ErrInvalid
	}
	if token != json.Delim('{') && token != json.Delim('[') {
		return nil
	}
	arrayItems := 0
	for decoder.More() {
		if token == json.Delim('[') {
			if arrayItems >= MaxArrayItems {
				return ErrExceeded
			}
			arrayItems++
		}
		if *remaining == 0 {
			return ErrExceeded
		}
		*remaining -= 1
		field := ""
		if token == json.Delim('{') {
			key, err := decoder.Token()
			if err != nil {
				return ErrInvalid
			}
			var ok bool
			field, ok = key.(string)
			if !ok {
				return ErrInvalid
			}
		}
		// These current DTO leaves are json.RawMessage values (or maps/arrays
		// of them). Provider bodies stay opaque; their keys and array lengths
		// must not be mistaken for owned Result/Exchange collection counts.
		schemaKey := mode == schemaFields
		opaqueValue := mode == rawJSONValues || (schemaKey && opaqueJSONField(field))
		if opaqueValue {
			var value json.RawMessage
			if err := decoder.Decode(&value); err != nil {
				return ErrInvalid
			}
			continue
		}
		childMode := schemaFields
		if mode == caseInputCases {
			childMode = rawJSONValues
		}
		if schemaKey && rawJSONCollection(field) {
			childMode = rawJSONValues
		}
		if schemaKey && field == "case_inputs" {
			childMode = caseInputCases
		}
		if schemaKey && ownedJSONMap(field) {
			childMode = ownedMapKeys
		}
		if err := scanDocument(
			decoder,
			remaining,
			depth+1,
			childMode,
		); err != nil {
			return err
		}
	}
	closing, err := decoder.Token()
	if err != nil {
		return ErrInvalid
	}
	want := json.Delim('}')
	if token == json.Delim('[') {
		want = json.Delim(']')
	}
	if closing != want {
		return ErrInvalid
	}
	return nil
}

func opaqueJSONField(field string) bool {
	switch field {
	case "body", "request_body", "response", "usage", "expected", "actual", "default", "value", "baseline":
		return true
	default:
		return false
	}
}

func rawJSONCollection(field string) bool {
	switch field {
	case "parameters", "events", "enum", "timeline", "distributions", "error_clusters":
		return true
	default:
		return false
	}
}

// Keys in these containers are user/provider-defined names, not DTO fields.
// Only the corresponding typed values resume field-name interpretation.
func ownedJSONMap(field string) bool {
	switch field {
	case "inputs", "definitions", "metrics", "dimensions", "sla", "thresholds":
		return true
	default:
		return false
	}
}
