// Package wanvideo registers the Wan video task lifecycle and observations.
package wanvideo

import (
	"encoding/json"
	"github.com/894x/llm-test-studio/internal/protocols/runtime"
)

func New() runtime.Module {
	return runtime.TaskProtocol{
		ID: "wan-video", Label: "Wan Video",
		SubmitPath: "/api/v1/services/aigc/video-generation/video-synthesis", PollPath: "/api/v1/tasks/",
		IDPointer: "/output/task_id", StatusPointer: "/output/task_status", UsagePointer: "/usage",
		ArtifactPointers: map[string]string{"video": "/output/video_url"},
		TerminalStatuses: []string{"SUCCEEDED", "FAILED", "CANCELED", "UNKNOWN"},
		DefaultBody:      json.RawMessage(`{"input":{"prompt":"A quiet landscape"}}`),
		Headers:          map[string]string{"X-DashScope-Async": "enable"},
	}
}
