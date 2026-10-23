package quicktest

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/894x/llm-test-studio/internal/domain"
)

func TestPerformanceBodyPreservesRequiredZeroValueFields(t *testing.T) {
	messages := []any{map[string]any{"role": "user", "content": ""}}
	for _, test := range []struct {
		protocol domain.Protocol
		expected map[string]any
	}{
		{
			protocol: domain.ProtocolOpenAIChat,
			expected: map[string]any{"messages": messages, "max_tokens": float64(0), "stream": true},
		},
		{
			protocol: domain.ProtocolOpenAIResponses,
			expected: map[string]any{"input": "", "max_output_tokens": float64(0), "stream": true},
		},
		{
			protocol: domain.ProtocolAnthropicMessages,
			expected: map[string]any{"messages": messages, "max_tokens": float64(0), "stream": true},
		},
	} {
		t.Run(string(test.protocol), func(t *testing.T) {
			body, err := performanceBody(test.protocol, "", 0, "")
			if err != nil {
				t.Fatal(err)
			}
			var actual map[string]any
			if err := json.Unmarshal(body, &actual); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(actual, test.expected) {
				t.Fatalf("body = %#v, want %#v", actual, test.expected)
			}
		})
	}
}

func BenchmarkPerformanceBody(b *testing.B) {
	prompt := "r0123456789abcdef" + strings.Repeat(" test", 255)
	for _, selected := range []domain.Protocol{
		domain.ProtocolOpenAIChat,
		domain.ProtocolOpenAIResponses,
		domain.ProtocolAnthropicMessages,
	} {
		for _, affinityKey := range []string{"", "performance-session-key"} {
			name := string(selected) + "/affinity=" + affinityKey
			b.Run(name, func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					body, err := performanceBody(selected, prompt, 128, affinityKey)
					if err != nil {
						b.Fatal(err)
					}
					if len(body) == 0 {
						b.Fatal("empty request body")
					}
				}
			})
		}
	}
}
