package client

import (
	"bytes"
	"compress/gzip"
	"io"
	"math"
	"mime"
	"net/http"
	"strings"
)

// Bound compressed bytes independently, including gzip headers and deflate
// overhead. Decoded documents keep their existing per-operation limit.
const gzipWireAllowance = 64 << 10

func readJSON(response *http.Response, limit int64) ([]byte, error) {
	values := response.Header.Values("Content-Type")
	if len(values) != 1 {
		return nil, ErrIntegrity
	}
	mediaType, parameters, err := mime.ParseMediaType(values[0])
	if err != nil || mediaType != "application/json" {
		return nil, ErrIntegrity
	}
	for name, value := range parameters {
		if name != "charset" || !strings.EqualFold(value, "utf-8") {
			return nil, ErrIntegrity
		}
	}
	if limit <= 0 || limit > math.MaxInt64-gzipWireAllowance-1 || response.Uncompressed {
		return nil, ErrIntegrity
	}
	encoding := response.Header.Values("Content-Encoding")
	compressed := len(encoding) == 1 && strings.EqualFold(encoding[0], "gzip")
	if !compressed && !identityEncoding(response) {
		return nil, ErrIntegrity
	}
	wireLimit := limit
	if compressed {
		wireLimit += gzipWireAllowance
	}
	if response.ContentLength < -1 || response.ContentLength > wireLimit {
		return nil, ErrIntegrity
	}
	wire, err := io.ReadAll(io.LimitReader(response.Body, wireLimit+1))
	if err != nil {
		return nil, transportError(err)
	}
	if int64(len(wire)) > wireLimit {
		return nil, ErrIntegrity
	}
	if response.ContentLength >= 0 && response.ContentLength != int64(len(wire)) {
		return nil, ErrIntegrity
	}
	if !compressed {
		return wire, nil
	}
	return decodeGzipJSON(wire, limit)
}

func decodeGzipJSON(wire []byte, limit int64) ([]byte, error) {
	input := bytes.NewReader(wire)
	reader, err := gzip.NewReader(input)
	if err != nil {
		return nil, ErrIntegrity
	}
	defer reader.Close()
	reader.Multistream(false)
	raw, err := io.ReadAll(io.LimitReader(reader, limit+1))
	// bytes.Reader implements io.ByteReader, so gzip consumes exactly one member.
	// Reading through EOF verifies its CRC/length; any remaining wire is invalid.
	if err != nil || int64(len(raw)) > limit || input.Len() != 0 {
		return nil, ErrIntegrity
	}
	return raw, nil
}
