// Package seedance registers the Seedance task lifecycle and observations.
package seedance

import (
	"encoding/json"
	"github.com/894x/llm-test-studio/internal/protocols/runtime"
)

func New() runtime.Module {
	return runtime.TaskProtocol{
		ID: "seedance", Label: "Seedance",
		SubmitPath: "/api/v3/contents/generations/tasks", PollPath: "/api/v3/contents/generations/tasks/",
		IDPointer: "/id", StatusPointer: "/status", UsagePointer: "/usage",
		ArtifactPointers: map[string]string{"video": "/content/video_url", "image": "/content/last_frame_url"},
		TerminalStatuses: []string{"succeeded", "failed", "cancelled", "expired"},
		DefaultBody:      json.RawMessage(`{"content":[{"type":"text","text":"A quiet landscape"}]}`),
	}
}
