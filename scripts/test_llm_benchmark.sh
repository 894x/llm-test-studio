#!/usr/bin/env bash

set -Eeuo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
TEST_DIR=$(mktemp -d)
MOCK_PID=""

cleanup() {
    if [[ -n "$MOCK_PID" ]]; then
        kill "$MOCK_PID" 2>/dev/null || true
        wait "$MOCK_PID" 2>/dev/null || true
    fi
    rm -rf -- "$TEST_DIR"
}
trap cleanup EXIT
trap 'exit 130' INT TERM

for command_name in curl jq go script; do
    command -v "$command_name" >/dev/null 2>&1 || {
        echo "缺少测试依赖: $command_name" >&2
        exit 1
    }
done

PORT_FILE="$TEST_DIR/mock.port"
go run "$SCRIPT_DIR/../cmd/mock-llm-server" \
    --host 127.0.0.1 \
    --port 0 \
    --port-file "$PORT_FILE" \
    --base-latency-ms 50 \
    --prefill-tps 500 \
    --decode-tps 25 \
    > "$TEST_DIR/mock.log" 2>&1 &
MOCK_PID=$!

ready=false
for _ in {1..50}; do
    if ! kill -0 "$MOCK_PID" 2>/dev/null; then
        break
    fi
    if [[ -f "$PORT_FILE" ]]; then
        PORT=$(tr -d '\r\n' < "$PORT_FILE")
    fi
    if [[ -n "${PORT:-}" ]] && curl --silent --fail --connect-timeout 0.2 --max-time 0.2 \
        "http://127.0.0.1:$PORT/health" >/dev/null; then
        ready=true
        break
    fi
    sleep 0.1
done

if [[ "$ready" != true ]]; then
    echo "Mock LLM 未能启动" >&2
    cat "$TEST_DIR/mock.log" >&2
    exit 1
fi

RESULT_FILE="$TEST_DIR/results.json"
SUMMARY_FILE="$TEST_DIR/results.summary.json"
LARGE_RESULT_FILE="$TEST_DIR/results-large.json"
TTY_RESULT_FILE="$TEST_DIR/results-tty.json"
TTY_LOG="$TEST_DIR/benchmark-tty.log"

COLUMNS=80 LLM_API_KEY=mock-key bash "$SCRIPT_DIR/llm_benchmark.sh" \
    -u "http://127.0.0.1:$PORT/v1" \
    -m mock-model \
    -n 6 \
    -c 1 \
    -i 40 \
    -o 6 \
    -d 5 \
    -s 1 \
    -f "$RESULT_FILE" \
    > "$TEST_DIR/benchmark.log" 2>&1

LLM_API_KEY=mock-key bash "$SCRIPT_DIR/llm_benchmark.sh" \
    -u "http://127.0.0.1:$PORT/v1" \
    -m mock-model \
    -n 1 \
    -c 1 \
    -i 400 \
    -o 10 \
    -d 5 \
    -s 0 \
    -f "$LARGE_RESULT_FILE" \
    > "$TEST_DIR/benchmark-large.log" 2>&1

script --quiet --return \
    --command "COLUMNS=80 LLM_API_KEY=mock-key bash '$SCRIPT_DIR/llm_benchmark.sh' -u 'http://127.0.0.1:$PORT/v1' -m mock-model -n 1 -c 1 -i 20 -o 2 -d 5 -s 0 --refresh-ms 50 -f '$TTY_RESULT_FILE'" \
    "$TTY_LOG" >/dev/null

cursor_hide_count=$(LC_ALL=C grep -aoF $'\033[?25l' "$TTY_LOG" | wc -l | tr -d ' ')
cursor_show_count=$(LC_ALL=C grep -aoF $'\033[?25h' "$TTY_LOG" | wc -l | tr -d ' ')
if (( cursor_hide_count != 1 || cursor_show_count != 1 )); then
    echo "TTY 压测应只隐藏和恢复光标各一次，实际 hide=$cursor_hide_count show=$cursor_show_count" >&2
    exit 1
fi

jq -e '
    .requested == 6 and
    .recorded == 6 and
    .successful == 6 and
    .failed == 0 and
    .timed_out == 0 and
    .incomplete_streams == 0 and
    .concurrency == 1 and
    .concurrency_mode == "open_loop" and
    .effective_concurrency_limit == null and
    .success_rate_percent == 100 and
    (.ttft_p50_ms >= 120 and .ttft_p50_ms <= 1000) and
    (.ttft_p90_ms >= 120 and .ttft_p90_ms <= 1000) and
    (.tpot_p50_ms >= 15 and .tpot_p50_ms <= 150) and
    (.tpot_p90_ms >= 15 and .tpot_p90_ms <= 150) and
    ([.http_statuses[] | select(.status == 200) | .count] == [6])
' "$SUMMARY_FILE" >/dev/null

actual_send_duration_ms=$(jq -r '.actual_send_duration_ms' "$SUMMARY_FILE")
if (( actual_send_duration_ms < 650 || actual_send_duration_ms > 1400 )); then
    echo "-s 1 未按发送窗口发完请求: 实际 ${actual_send_duration_ms}ms" >&2
    exit 1
fi

jq -e '
    length == 6 and
    all(.[];
        .success == true and
        .stream_done == true and
        .http_status == 200 and
        .ttft_ms != null and
        .tpot_ms != null and
        (.response_body | contains("data: [DONE]")) and
        (.request.headers.authorization == "[REDACTED]")
    )
' "$RESULT_FILE" >/dev/null

if ! jq -e 'all(.[]; .request.body.max_tokens == 6 and .request.body.max_completion_tokens == 6)' \
    "$RESULT_FILE" >/dev/null; then
    echo "-o 6 没有同时写入 max_tokens 和 max_completion_tokens" >&2
    exit 1
fi

jq -e '
    length == 1 and
    .[0].success == true and
    .[0].usage.prompt_tokens >= 350 and
    .[0].usage.completion_tokens == 10 and
    .[0].ttft_ms >= 700 and
    .[0].duration_ms >= 1050
' "$LARGE_RESULT_FILE" >/dev/null

if grep -R --quiet --fixed-strings 'mock-key' "$RESULT_FILE" "$SUMMARY_FILE"; then
    echo "测试产物泄露了 API Key" >&2
    exit 1
fi

grep --quiet --fixed-strings 'TTFT P50/P90' "$TEST_DIR/benchmark.log"
grep --quiet --fixed-strings 'TPOT P50/P90' "$TEST_DIR/benchmark.log"
grep --quiet --fixed-strings '成功率' "$TEST_DIR/benchmark.log"
grep --quiet --fixed-strings '成功 6 | 失败 0' "$TEST_DIR/benchmark.log"
grep --quiet --fixed-strings '进度 [' "$TEST_DIR/benchmark.log"
grep --quiet --fixed-strings '完成 6 | 等待 0 | 未发送 0' "$TEST_DIR/benchmark.log"
grep --quiet --fixed-strings '指标 | 完成 6 | 等待 0 | 未发送 0 | 成功 6 | 失败 0' "$TEST_DIR/benchmark.log"
grep --quiet --fixed-strings '开放式定时发送（-c 1 不限制在途数）' "$TEST_DIR/benchmark.log"
grep --quiet --extended-regexp '[▏▎▍▌▋▊▉]' "$TEST_DIR/benchmark.log"

progress_bar=$(sed -n 's/^进度 \[\([█▏▎▍▌▋▊▉-]*\)\]$/\1/p' "$TEST_DIR/benchmark.log" | head -n 1)
if (( ${#progress_bar} != 73 )); then
    echo "进度条没有铺满 80 列终端: 实际 ${#progress_bar} 格，期望 73 格" >&2
    exit 1
fi

progress_updates=$(grep --count '^进度 \[' "$TEST_DIR/benchmark.log")
if (( progress_updates < 10 )); then
    echo "进度刷新频率过低: 约 2 秒测试仅输出 ${progress_updates} 次进度" >&2
    exit 1
fi

if grep --quiet --fixed-strings '"run_started_at"' "$TEST_DIR/benchmark.log"; then
    echo "压测结束后不应打印整份汇总 JSON" >&2
    exit 1
fi

echo "PASS: 本地 Mock SSE 压测测试通过"
jq '{
    successful,
    success_rate_percent,
    ttft_p50_ms,
    ttft_p90_ms,
    tpot_p50_ms,
    tpot_p90_ms,
    actual_send_duration_ms,
    drain_duration_ms
}' "$SUMMARY_FILE"
