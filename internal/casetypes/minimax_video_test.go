package casetypes

import (
	"encoding/json"
	"testing"

	"github.com/894x/llm-test-studio/internal/domain"
)

func TestLegacyAPIAuditValidatesMiniMaxVideoTaskKinds(t *testing.T) {
	registry := MustBuiltinRegistry()
	for _, kind := range []string{"minimax_video_task_success", "minimax_video_task_rejected", "minimax_video_auth_rejected"} {
		definition := domain.TestCaseDefinition{
			SchemaVersion: domain.CurrentTestCaseDefinitionSchemaVersion,
			Type:          TypeLegacyAPIAudit, TypeVersion: 1,
			Spec: json.RawMessage(`{"kind":"` + kind + `","request":{"method":"POST","path":"/v2/video_generation","headers":{},"body":{"content":[{"type":"text","text":"cat"}],"resolution":"768P","duration":4,"ratio":"16:9"}},"options":{}}`),
		}
		if err := registry.Validate(domain.Protocol("minimax-video"), definition); err != nil {
			t.Fatalf("Validate(%s) error = %v", kind, err)
		}
	}
}
