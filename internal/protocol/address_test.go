package protocol_test

import (
	"testing"

	"github.com/894x/llm-test-studio/internal/protocol"
)

func TestResolveAddressCompletesOnlyMissingRoute(t *testing.T) {
	tests := []struct {
		input, path, want, suffix string
	}{
		{path: protocol.OpenAIChatPath, want: protocol.OpenAIChatPath, suffix: protocol.OpenAIChatPath},
		{input: "/", path: protocol.OpenAIChatPath, want: protocol.OpenAIChatPath, suffix: "v1/chat/completions"},
		{input: "/v1", path: protocol.OpenAIChatPath, want: protocol.OpenAIChatPath, suffix: "/chat/completions"},
		{input: "/v1/", path: protocol.OpenAIChatPath, want: protocol.OpenAIChatPath, suffix: "chat/completions"},
		{input: "/v1/ch", path: protocol.OpenAIChatPath, want: protocol.OpenAIChatPath, suffix: "at/completions"},
		{input: "/v1/chat/completions", path: protocol.OpenAIChatPath, want: protocol.OpenAIChatPath},
		{input: "/v1/chat/completions///", path: protocol.OpenAIChatPath, want: protocol.OpenAIChatPath},
		{input: "/chat/completions", path: protocol.OpenAIChatPath, want: "/chat/completions"},
		{input: "/proxy", path: protocol.OpenAIChatPath,
			want: "/proxy/v1/chat/completions", suffix: protocol.OpenAIChatPath},
		{input: "/proxy/v1", path: protocol.OpenAIChatPath,
			want: "/proxy/v1/chat/completions", suffix: "/chat/completions"},
		{input: "/proxy/v1/chat/completions", path: protocol.OpenAIChatPath, want: "/proxy/v1/chat/completions"},
		{input: "/proxy/v1/chat/completions", path: protocol.OpenAIModelsPath, want: "/proxy/v1/models"},
		{input: "/proxy/v1/models", path: protocol.OpenAIChatPath, want: "/proxy/v1/chat/completions"},
	}
	descriptor, _ := protocol.Lookup(protocol.OpenAIChat)
	for _, test := range tests {
		t.Run(test.input+"->"+test.path, func(t *testing.T) {
			got, err := protocol.ResolveAddress("https://example.test"+test.input, test.path, descriptor.RequestPaths)
			if err != nil || got.Endpoint != "https://example.test"+test.want || got.Suffix != test.suffix {
				t.Fatalf("resolved = %+v, error = %v", got, err)
			}
		})
	}
}

func TestResolveVideoSubmitAddressRetainsServicePrefixForPolling(t *testing.T) {
	descriptor, _ := protocol.Lookup(protocol.Seedance)
	address := "https://example.test/proxy" + protocol.SeedancePath
	got, err := protocol.ResolveAddress(address, protocol.SeedancePath+"/task-1", descriptor.RequestPaths)
	if err != nil || got.Endpoint != address+"/task-1" {
		t.Fatalf("poll address = %+v, error = %v", got, err)
	}
}

func TestResolveAddressRejectsUnsafeInputs(t *testing.T) {
	for _, address := range []string{
		"", "example.test", "https://example.test ", "ftp://example.test", "https://user:key@example.test",
		"https://example.test?", "https://example.test?key=secret", "https://example.test#", "https://example.test#secret",
		"https://example.test\\v1",
	} {
		if _, err := protocol.ResolveAddress(address, protocol.OpenAIChatPath, nil); err == nil {
			t.Errorf("unsafe address accepted: %q", address)
		}
	}
	for _, path := range []string{
		"", "/", "//different.test/chat/completions", "https://different.test/chat/completions",
		"/v1/chat/completions?key=secret", "/v1/chat/completions?", "/v1/chat/completions#", "/v1\\chat/completions",
	} {
		if _, err := protocol.ResolveAddress("https://example.test", path, nil); err == nil {
			t.Errorf("unsafe path accepted: %q", path)
		}
	}
}
