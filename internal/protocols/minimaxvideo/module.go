// Package minimaxvideo registers the MiniMax video task lifecycle and observations.
package minimaxvideo

import (
	"encoding/json"

	"github.com/894x/llm-test-studio/internal/protocol"
	"github.com/894x/llm-test-studio/internal/protocols/runtime"
)

func New() runtime.Module {
	return runtime.TaskProtocol{
		ID: "minimax-video", Label: "MiniMax Video",
		SubmitPath: protocol.MiniMaxVideoPath, PollPath: "/v2/query/video_generation/",
		IDPointer: "/task_id", StatusPointer: "/task/status", UsagePointer: "/task/usage",
		ArtifactPointers: map[string]string{"video": "/task/content/url"},
		TerminalStatuses: []string{"succeeded", "failed", "cancelled"},
		DefaultBody:      json.RawMessage(`{"content":[{"type":"text","text":"A quiet landscape"}]}`),
	}
}
