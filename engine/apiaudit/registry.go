package apiaudit

import (
	"context"
	"fmt"
	"slices"

	"github.com/894x/llm-test-studio/internal/protocol"
)

type caseRunner func(context.Context, HTTPDoer, RunConfig, PlannedRun) CaseResult

type driver struct {
	kinds []string
	run   caseRunner
}

var chatKinds = []string{
	"manual_unknown", "id_consistency", "stream_usage", "chat_stream", "error_schema", "models_contains",
	"response_id", "stop_parameter", "structured_json", "tool_call", "chat_sync", "usage_baseline",
	"stream_parity", "sse_integrity", "reasoning_visibility", "logprobs_contract", "deterministic_stability",
	"multi_turn_usage", "structured_stability", "concurrency_probe", "stream_ttft", "long_stream_stability",
	"cache_visibility", "stream_throughput", "sampling_effect", "parameter_boundaries", "usage_growth",
	"needle_retrieval", "error_no_usage", "padding_ratio",
}

// Each legacy driver declares both its accepted kinds and its implementation.
// Filesystem loading, typed Case validation, CLI, and desktop use this registry.
var drivers = map[string]driver{
	protocol.OpenAIChat:   {kinds: chatKinds, run: singleCaseRunner(RunOpenAIChatCase)},
	protocol.KimiK3:       {kinds: append(slices.Clone(chatKinds), "kimi_success", "kimi_tool_call", "kimi_reasoning_visible", "kimi_reasoning_hidden", "kimi_error_400"), run: singleCaseRunner(RunKimiK3Case)},
	protocol.Seedance:     {kinds: []string{"seedance_task"}, run: RunSeedanceCase},
	protocol.WanVideo:     {kinds: []string{"wan_task_success", "wan_task_rejected"}, run: RunWanVideoCase},
	protocol.MiniMaxVideo: {kinds: []string{"minimax_video_task_success", "minimax_video_task_rejected", "minimax_video_auth_rejected"}, run: RunMiniMaxVideoCase},
}

func SupportedProtocols() []string {
	result := make([]string, 0, len(drivers))
	for _, descriptor := range protocol.All() {
		if _, ok := drivers[descriptor.ID]; ok {
			result = append(result, descriptor.ID)
		}
	}
	return result
}

func SupportsKind(protocolID, kind string) bool {
	driver, ok := drivers[protocolID]
	return ok && slices.Contains(driver.kinds, kind)
}

func RunCase(ctx context.Context, doer HTTPDoer, config RunConfig, planned PlannedRun) (CaseResult, error) {
	driver, ok := drivers[config.Suite]
	if !ok {
		return CaseResult{}, fmt.Errorf("unsupported execution protocol %q", config.Suite)
	}
	if planned.Case.Protocol != config.Suite || !SupportsKind(config.Suite, planned.Case.Kind) {
		return CaseResult{}, fmt.Errorf("case %s is not supported by protocol %s", planned.Case.ID, config.Suite)
	}
	return driver.run(ctx, doer, config, planned), nil
}

func singleCaseRunner(run func(context.Context, HTTPDoer, RunConfig, CaseDefinition) CaseResult) caseRunner {
	return func(ctx context.Context, doer HTTPDoer, config RunConfig, planned PlannedRun) CaseResult {
		config.Model = planned.Model
		result := run(ctx, doer, config, planned.Case)
		result.ID = planned.ResultID
		return result
	}
}
