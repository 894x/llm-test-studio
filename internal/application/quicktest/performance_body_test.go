package quicktest

import (
	"strings"
	"testing"

	"github.com/894x/llm-test-studio/internal/domain"
)

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
