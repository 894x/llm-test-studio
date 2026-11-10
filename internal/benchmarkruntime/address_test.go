package benchmarkruntime

import (
	"testing"

	"github.com/894x/llm-test-studio/internal/domain"
)

func TestInspectSDKBaseURLsMatchSharedProtocolRoutes(t *testing.T) {
	tests := []struct {
		address  string
		protocol domain.Protocol
		want     string
	}{
		{address: "https://api.example.test", protocol: domain.ProtocolOpenAIChat, want: "https://api.example.test/v1"},
		{address: "https://api.example.test/v1", protocol: domain.ProtocolOpenAIChat, want: "https://api.example.test/v1"},
		{address: "https://api.example.test/v1/chat/completions", protocol: domain.ProtocolOpenAIChat, want: "https://api.example.test/v1"},
		{address: "https://api.example.test/v1/responses", protocol: domain.ProtocolOpenAIResponses, want: "https://api.example.test/v1"},
		{address: "https://api.example.test/v1", protocol: domain.ProtocolAnthropicMessages, want: "https://api.example.test"},
		{address: "https://api.example.test/proxy/v1/messages", protocol: domain.ProtocolAnthropicMessages, want: "https://api.example.test/proxy"},
	}
	for _, tt := range tests {
		t.Run(tt.address+string(tt.protocol), func(t *testing.T) {
			got, err := inspectBaseURL(tt.address, tt.protocol)
			if err != nil || got != tt.want {
				t.Fatalf("base = %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}
