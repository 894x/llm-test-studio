package remoteresults

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

const (
	MaxEventCollectionItems  = 32_768
	MaxReportCollectionItems = 131_072
	// 10000 samples may contribute up to 30000 sparse time slices. Preserve
	// their complete DTOs within the independent 32 MiB archive byte budget.
	MaxPerformanceCollectionItems = 4_194_304
	maxDocumentDepth              = 128
)

type scanMode uint8

const (
	schemaFields scanMode = iota
	rawJSONValues
	caseInputCases
	ownedMapKeys
)

var ErrCollectionBudget = errors.New("remote result exceeds collection budget")

// CheckEventBudget bounds allocations before any typed domain decoding.
func CheckEventBudget(raw []byte) error { return checkDocumentBudget(raw, MaxEventCollectionItems) }
func CheckRunReportBudget(raw []byte) error {
	return checkDocumentBudget(raw, MaxReportCollectionItems)
}

func CheckPerformanceBudget(raw []byte) error {
	return checkDocumentBudget(raw, MaxPerformanceCollectionItems)
}

func checkDocumentBudget(raw []byte, limit int) error {
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
	if depth > maxDocumentDepth {
		return ErrCollectionBudget
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
			if arrayItems >= MaxReportCollectionItems {
				return ErrCollectionBudget
			}
			arrayItems++
		}
		if *remaining == 0 {
			return ErrCollectionBudget
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
