package jobfiles

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"strings"
)

const maxImageDimension = 4096
const maxImagePixels = 16_777_216

type selectionCommand struct {
	DataBase64 string
	MediaType  string
}

func decodeSelection(document string) (selectionCommand, int64, error) {
	if len(document) > MaxSelectionDocumentBytes {
		return selectionCommand{}, 0, ErrTooLarge
	}
	decoder := json.NewDecoder(strings.NewReader(document))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return selectionCommand{}, 0, ErrInvalid
	}
	seen := map[string]struct{}{}
	var command selectionCommand
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return selectionCommand{}, 0, ErrInvalid
		}
		name, ok := token.(string)
		if !ok {
			return selectionCommand{}, 0, ErrInvalid
		}
		if _, exists := seen[name]; exists {
			return selectionCommand{}, 0, ErrInvalid
		}
		seen[name] = struct{}{}
		switch name {
		case "data_base64":
			err = decoder.Decode(&command.DataBase64)
		case "media_type":
			err = decoder.Decode(&command.MediaType)
		default:
			return selectionCommand{}, 0, ErrInvalid
		}
		if err != nil {
			return selectionCommand{}, 0, ErrInvalid
		}
	}
	closing, err := decoder.Token()
	if err != nil || closing != json.Delim('}') || len(seen) != 2 {
		return selectionCommand{}, 0, ErrInvalid
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return selectionCommand{}, 0, ErrInvalid
	}
	if command.MediaType != "image/png" && command.MediaType != "image/jpeg" {
		return selectionCommand{}, 0, ErrInvalid
	}
	size, err := decodedSize(command.DataBase64)
	return command, size, err
}

func decodedSize(encoded string) (int64, error) {
	if len(encoded) > base64.StdEncoding.EncodedLen(MaxSelectionBytes) {
		return 0, ErrTooLarge
	}
	if len(encoded) == 0 || len(encoded)%4 != 0 {
		return 0, ErrInvalid
	}
	padding := 0
	for index := len(encoded) - 1; index >= 0 && encoded[index] == '='; index-- {
		padding++
	}
	if padding > 2 {
		return 0, ErrInvalid
	}
	for index := range len(encoded) {
		character := encoded[index]
		letter := character >= 'A' && character <= 'Z' || character >= 'a' && character <= 'z'
		digit := character >= '0' && character <= '9'
		punctuation := character == '+' || character == '/'
		validCharacter := letter || digit || punctuation
		validPadding := character == '=' && index >= len(encoded)-padding
		if !validCharacter && !validPadding {
			return 0, ErrInvalid
		}
	}
	size := int64(len(encoded)/4*3 - padding)
	if size < 1 {
		return 0, ErrInvalid
	}
	if size > MaxSelectionBytes {
		return 0, ErrTooLarge
	}
	return size, nil
}

func decodeImage(ctx context.Context, command selectionCommand, size int64) ([]byte, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	data, err := base64.StdEncoding.Strict().DecodeString(command.DataBase64)
	if err != nil || int64(len(data)) != size {
		clear(data)
		return nil, "", ErrInvalid
	}
	var configuration image.Config
	switch command.MediaType {
	case "image/png":
		configuration, err = png.DecodeConfig(bytes.NewReader(data))
	case "image/jpeg":
		configuration, err = jpeg.DecodeConfig(bytes.NewReader(data))
	}
	positiveDimensions := configuration.Width > 0 && configuration.Height > 0
	boundedDimensions := configuration.Width <= maxImageDimension && configuration.Height <= maxImageDimension
	if err != nil || !positiveDimensions || !boundedDimensions {
		clear(data)
		return nil, "", ErrInvalid
	}
	if int64(configuration.Width)*int64(configuration.Height) > maxImagePixels {
		clear(data)
		return nil, "", ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		clear(data)
		return nil, "", err
	}
	digest := sha256.Sum256(data)
	return data, hex.EncodeToString(digest[:]), nil
}
