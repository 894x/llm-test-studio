// Package common contains the small JSON boundary needed by the extracted
// compatibility engine. It deliberately has no dependency on new-api.
package common

import (
	"encoding/json"
	"io"
)

func Marshal(value any) ([]byte, error) { return json.Marshal(value) }

func Unmarshal(data []byte, value any) error { return json.Unmarshal(data, value) }

func DecodeJson(reader io.Reader, value any) error {
	return json.NewDecoder(reader).Decode(value)
}
