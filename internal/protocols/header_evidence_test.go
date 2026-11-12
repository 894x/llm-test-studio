package protocols

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestHeaderEvidenceRedactsCredentialsPreservesTTLAndBoundsSize(t *testing.T) {
	headers := http.Header{
		"Authorization":                   {"Bearer private-test-key"},
		"Proxy-Authorization":             {"private-proxy"},
		"X-Api-Key":                       {"private-key"},
		"X-Auth-Token":                    {"private-token"},
		"Set-Cookie":                      {"session=private-cookie"},
		"Msh-Usage-Cache-Write-Tokens-5m": {"3584"},
		"Msh-Usage-Cache-Write-Tokens-1h": {"0"},
		"X-Request-Id":                    {"first", "second"},
		"X-Echo":                          {"private-test-key"},
		"Location":                        {"https://cache.test/file?signature=private-url"},
		"X-Large":                         {strings.Repeat("x", 4097)},
	}
	evidence := captureHeaderEvidence(headers, "private-test-key")
	encoded, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "private-") {
		t.Fatal("credential leaked into captured headers")
	}
	if evidence.Values["Msh-Usage-Cache-Write-Tokens-5m"][0] != "3584" ||
		evidence.Values["Msh-Usage-Cache-Write-Tokens-1h"][0] != "0" {
		t.Fatal("TTL counters were confused with credential tokens")
	}
	if len(evidence.Values["X-Request-Id"]) != 2 || !evidence.Truncated {
		t.Fatal("header multiplicity or truncation evidence lost")
	}
	if headers.Get("Authorization") != "Bearer private-test-key" {
		t.Fatal("capturing headers mutated the actual request")
	}
	if empty := captureHeaderEvidence(http.Header{}, ""); empty == nil || empty.Values == nil || len(empty.Values) != 0 {
		t.Fatal("captured empty headers must differ from not captured")
	}
}
