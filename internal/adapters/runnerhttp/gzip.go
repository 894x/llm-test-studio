package runnerhttp

import (
	"bytes"
	"compress/gzip"
	"mime"
	"net/http"
	"strconv"
	"strings"
)

type jsonResponseWriter struct {
	http.ResponseWriter
	gzip bool
}

func (writer *jsonResponseWriter) Unwrap() http.ResponseWriter { return writer.ResponseWriter }

func acceptsJSONGzip(request *http.Request) bool {
	if request.Header.Get("Range") != "" {
		return false
	}
	found, accepted := false, false
	for _, value := range request.Header.Values("Accept-Encoding") {
		for _, item := range strings.Split(value, ",") {
			coding, parameters, err := mime.ParseMediaType(strings.TrimSpace(item))
			if err != nil || !strings.EqualFold(coding, "gzip") {
				continue
			}
			if found {
				return false
			}
			found = true
			quality := 1.0
			if raw, present := parameters["q"]; present {
				quality, err = strconv.ParseFloat(raw, 64)
			}
			accepted = err == nil && len(parameters) <= 1 && quality > 0 && quality <= 1
			if len(parameters) == 1 && parameters["q"] == "" {
				accepted = false
			}
		}
	}
	return accepted
}

func gzipJSON(document []byte) []byte {
	var buffer bytes.Buffer
	writer := gzip.NewWriter(&buffer)
	// bytes.Buffer cannot fail a write; Close only flushes to that buffer.
	_, _ = writer.Write(document)
	_ = writer.Close()
	return buffer.Bytes()
}
