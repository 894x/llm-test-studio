#!/usr/bin/env bash
# OpenAI Chat Completions 流式并发压测脚本。
#
# 时间语义：
#   --duration       单个请求从实际发出开始计算的超时时间。
#   --send-duration  将全部请求均匀发出的目标时间窗口；0 表示突发发送。
#
# 每个请求会立即保存为独立 JSON，全部结束后再生成结果数组和汇总文件。
# 运行期间会以两行显示三段进度条、成功率、TTFT P50/P90 和 TPOT P50/P90。

set -Eeuo pipefail
shopt -s nullglob

API_BASE=""
API_KEY="${LLM_API_KEY:-}"
MODEL=""
REQUEST_TIMEOUT=60
SEND_DURATION=0
INPUT_TOKEN_LIMIT=100
OUTPUT_TOKEN_LIMIT=50
NUM_REQUESTS=10
MAX_CONCURRENT=10
PROGRESS_REFRESH_MS=50
OUTPUT_FILE="results.json"
STREAM=true

usage() {
    local exit_code="${1:-0}"
    cat <<'EOF'
用法:
  ./scripts/llm_benchmark.sh -u <API_BASE> -m <MODEL> [选项]

必需参数:
  -u, --api-base       API 基础地址，例如 https://example.com/v1
  -m, --model          模型 ID
  -k, --key            API Key；也可使用环境变量 LLM_API_KEY

可选参数:
  -d, --duration       单请求超时时间（秒），默认 60
  -s, --send-duration  发完全部请求的目标窗口（秒），默认 0，即突发发送
  -i, --input-token    输入 prompt 的近似 token 目标，默认 100
  -o, --output-token   同时设置 max_tokens/max_completion_tokens，默认 50
  -n, --num-requests   总请求数，默认 10
  -c, --concurrency    突发模式（-s 0）的最大在途请求数，默认 10，最高 2000
      --refresh-ms     进度刷新间隔（毫秒），默认 50，最低 10
  -f, --output-file    汇总结果 JSON，默认 results.json
      --no-stream      禁用流式响应
  -h, --help           显示帮助

示例:
  LLM_API_KEY='sk-...' ./scripts/llm_benchmark.sh \
    -u https://example.com/v1 \
    -m kimi-k3 \
    -n 2000 -c 2000 \
    -i 64000 -o 100 \
    -d 240 -s 240

上述示例表示：在 240 秒内均匀发出 2000 个请求，每个请求独立拥有
240 秒超时；最后一个请求发出后继续等待在途请求排空。只要 -s 大于 0，
脚本就采用开放式定时发送，-c 不限制在途数。请求默认使用流式响应；
进度条以绿色表示完成、黄色表示等待，并实时显示性能指标。
EOF
    exit "$exit_code"
}

require_value() {
    local option="$1"
    local remaining="$2"
    if (( remaining < 2 )); then
        echo "错误: 参数 $option 缺少值" >&2
        exit 1
    fi
}

while (( $# > 0 )); do
    case "$1" in
        -u|--api-base)
            require_value "$1" "$#"
            API_BASE="$2"
            shift 2
            ;;
        -k|--key)
            require_value "$1" "$#"
            API_KEY="$2"
            shift 2
            ;;
        -m|--model)
            require_value "$1" "$#"
            MODEL="$2"
            shift 2
            ;;
        -d|--duration)
            require_value "$1" "$#"
            REQUEST_TIMEOUT="$2"
            shift 2
            ;;
        -s|--send-duration)
            require_value "$1" "$#"
            SEND_DURATION="$2"
            shift 2
            ;;
        -i|--input-token)
            require_value "$1" "$#"
            INPUT_TOKEN_LIMIT="$2"
            shift 2
            ;;
        -o|--output-token)
            require_value "$1" "$#"
            OUTPUT_TOKEN_LIMIT="$2"
            shift 2
            ;;
        -n|--num-requests)
            require_value "$1" "$#"
            NUM_REQUESTS="$2"
            shift 2
            ;;
        -c|--concurrency)
            require_value "$1" "$#"
            MAX_CONCURRENT="$2"
            shift 2
            ;;
        --refresh-ms)
            require_value "$1" "$#"
            PROGRESS_REFRESH_MS="$2"
            shift 2
            ;;
        -f|--output-file)
            require_value "$1" "$#"
            OUTPUT_FILE="$2"
            shift 2
            ;;
        --no-stream)
            STREAM=false
            shift
            ;;
        -h|--help)
            usage 0
            ;;
        *)
            echo "错误: 未知参数 $1" >&2
            usage 1
            ;;
    esac
done

if [[ -z "$API_BASE" || -z "$API_KEY" || -z "$MODEL" ]]; then
    echo "错误: 必须提供 API Base、API Key 和模型 ID" >&2
    usage 1
fi

for value in "$REQUEST_TIMEOUT" "$INPUT_TOKEN_LIMIT" "$OUTPUT_TOKEN_LIMIT" "$NUM_REQUESTS" "$MAX_CONCURRENT"; do
    if [[ ! "$value" =~ ^[1-9][0-9]*$ ]]; then
        echo "错误: 超时、token 数、请求数和并发数必须是正整数" >&2
        exit 1
    fi
done

if [[ ! "$SEND_DURATION" =~ ^[0-9]+$ ]]; then
    echo "错误: send-duration 必须是非负整数" >&2
    exit 1
fi

if [[ ! "$PROGRESS_REFRESH_MS" =~ ^[1-9][0-9]*$ ]] ||
   (( PROGRESS_REFRESH_MS < 10 )); then
    echo "错误: refresh-ms 必须是大于等于 10 的正整数" >&2
    exit 1
fi

printf -v PROGRESS_REFRESH_SECONDS '%d.%03d' \
    "$((PROGRESS_REFRESH_MS / 1000))" "$((PROGRESS_REFRESH_MS % 1000))"

if (( MAX_CONCURRENT > 2000 )); then
    echo "警告: 并发数超过 2000，自动限制为 2000" >&2
    MAX_CONCURRENT=2000
fi

if (( MAX_CONCURRENT > NUM_REQUESTS )); then
    MAX_CONCURRENT="$NUM_REQUESTS"
fi

if (( SEND_DURATION > 0 )); then
    CONCURRENCY_MODE="open_loop"
    EFFECTIVE_CONCURRENCY_LIMIT=0
    FD_CONCURRENCY="$NUM_REQUESTS"
else
    CONCURRENCY_MODE="bounded_burst"
    EFFECTIVE_CONCURRENCY_LIMIT="$MAX_CONCURRENT"
    FD_CONCURRENCY="$MAX_CONCURRENT"
fi

for command_name in curl jq awk grep sed mktemp; do
    if ! command -v "$command_name" >/dev/null 2>&1; then
        echo "错误: 缺少依赖 $command_name" >&2
        exit 1
    fi
done

MAX_OPEN_FILES=$(ulimit -n)
if [[ "$MAX_OPEN_FILES" =~ ^[0-9]+$ ]] &&
   (( MAX_OPEN_FILES < FD_CONCURRENCY * 2 + 100 )); then
    echo "警告: 当前文件描述符限制为 $MAX_OPEN_FILES；当前发送模式最多可能有 $FD_CONCURRENCY 个在途请求，建议提高 ulimit -n" >&2
fi

API_BASE="${API_BASE%/}"
case "$API_BASE" in
    */v1/chat/completions|*/chat/completions)
        REQUEST_URL="$API_BASE"
        ;;
    */v1)
        REQUEST_URL="$API_BASE/chat/completions"
        ;;
    *)
        REQUEST_URL="$API_BASE/v1/chat/completions"
        ;;
esac

OUTPUT_STEM="${OUTPUT_FILE%.json}"
RUN_ID="$(date -u +%Y%m%dT%H%M%SZ)-$$"
ARTIFACT_DIR="${OUTPUT_STEM}.artifacts/${RUN_ID}"
SUMMARY_FILE="${OUTPUT_STEM}.summary.json"
mkdir -p "$ARTIFACT_DIR"

TMP_DIR=$(mktemp -d)
MAIN_BASHPID=$BASHPID
CURSOR_HIDDEN=false
cleanup() {
    if (( BASHPID != MAIN_BASHPID )); then
        return
    fi
    if [[ "$CURSOR_HIDDEN" == true ]]; then
        printf '\033[?25h'
        CURSOR_HIDDEN=false
    fi
    if [[ -n "${TMP_DIR:-}" && -d "$TMP_DIR" ]]; then
        rm -rf -- "$TMP_DIR"
    fi
}
trap cleanup EXIT INT TERM

PROMPT_FILE="$TMP_DIR/common_prompt.txt"
REQUEST_BODY_FILE="$TMP_DIR/request_body.json"
CURL_CONFIG_FILE="$TMP_DIR/curl-auth.conf"
LIVE_METRICS_DIR="$TMP_DIR/live-metrics"
LIVE_LAUNCHED_DIR="$TMP_DIR/live-launched"
MONITOR_STOP_FILE="$TMP_DIR/monitor.stop"
mkdir -p "$LIVE_METRICS_DIR" "$LIVE_LAUNCHED_DIR"

if [[ "$API_KEY" == *$'\n'* || "$API_KEY" == *$'\r'* ]]; then
    echo "错误: API Key 包含非法换行符" >&2
    exit 1
fi

ESCAPED_API_KEY="${API_KEY//\\/\\\\}"
ESCAPED_API_KEY="${ESCAPED_API_KEY//\"/\\\"}"
printf 'header = "Authorization: Bearer %s"\n' "$ESCAPED_API_KEY" > "$CURL_CONFIG_FILE"
chmod 600 "$CURL_CONFIG_FILE"

# 通用 tokenizer 无法精确计算任意模型的 token。重复常见单词比随机 ASCII
# 更接近稳定的一词一 token；实际数量仍以响应 usage.prompt_tokens 为准。
{
    printf 'Continue writing useful text until the output token limit.'
    REPEAT_COUNT=$((INPUT_TOKEN_LIMIT > 16 ? INPUT_TOKEN_LIMIT - 16 : 1))
    for ((prompt_index = 0; prompt_index < REPEAT_COUNT; prompt_index++)); do
        printf ' load'
    done
} > "$PROMPT_FILE"

jq -n \
    --arg model "$MODEL" \
    --argjson max_tokens "$OUTPUT_TOKEN_LIMIT" \
    --argjson stream "$STREAM" \
    --rawfile content "$PROMPT_FILE" \
    '{
        model: $model,
        messages: [{role: "user", content: $content}],
        max_tokens: $max_tokens,
        max_completion_tokens: $max_tokens,
        stream: $stream
    }
    | if $stream then
        . + {stream_options: {include_usage: true}}
      else
        .
      end' > "$REQUEST_BODY_FILE"

RUN_STARTED_AT=$(date -u +%Y-%m-%dT%H:%M:%S.%3NZ)
RUN_START_NS=$(date +%s%N)

run_request() {
    local request_index="$1"
    local scheduled_offset_ns="$2"
    local request_dir="$TMP_DIR/request-$request_index"
    local response_file="$request_dir/response_body"
    local error_file="$request_dir/curl_error"
    local curl_metrics_file="$request_dir/curl_metrics"
    local first_token_file="$request_dir/first_token_ns"
    local artifact_file
    local artifact_tmp
    local live_metric_file
    local live_metric_tmp
    local started_at
    local start_ns
    local end_ns
    local finished_at
    local elapsed_ms
    local started_offset_ms
    local finished_offset_ms
    local metrics
    local curl_exit
    local pipeline_status
    local http_status
    local time_start_transfer
    local time_total
    local error_message
    local stream_done=false
    local success=false
    local timed_out=false
    local usage_json='{}'
    local first_token_ns=""
    local ttft_ms='null'
    local tpot_ms='null'
    local completion_tokens=""

    mkdir -p "$request_dir"
    : > "$response_file"
    : > "$error_file"
    : > "$curl_metrics_file"
    : > "$first_token_file"

    started_at=$(date -u +%Y-%m-%dT%H:%M:%S.%3NZ)
    start_ns=$(date +%s%N)

    set +e
    curl \
            --config "$CURL_CONFIG_FILE" \
            --no-buffer \
            --silent \
            --show-error \
            --request POST \
            --header "Content-Type: application/json" \
            --header "Accept: application/json, text/event-stream" \
            --data-binary "@$REQUEST_BODY_FILE" \
            --connect-timeout 30 \
            --max-time "$REQUEST_TIMEOUT" \
            --write-out $'\n__CURL_METRICS__\t%{http_code}\t%{time_starttransfer}\t%{time_total}\n' \
            "$REQUEST_URL" \
            2> "$error_file" \
        | while IFS= read -r response_line || [[ -n "$response_line" ]]; do
            normalized_line="${response_line%$'\r'}"

            if [[ "$normalized_line" == "__CURL_METRICS__"$'\t'* ]]; then
                printf '%s\n' "${normalized_line#*$'\t'}" > "$curl_metrics_file"
                continue
            fi

            printf '%s\n' "$response_line" >> "$response_file"

            if [[ ! -s "$first_token_file" && "$normalized_line" == data:* ]]; then
                event_received_ns=$(date +%s%N)
                event_data="${normalized_line#data:}"
                event_data="${event_data#${event_data%%[![:space:]]*}}"

                if [[ "$event_data" != "[DONE]" ]] &&
                   jq -e '
                       any(.choices[]?;
                           ((.delta.content? // "") | type == "string" and length > 0) or
                           ((.delta.reasoning_content? // "") | type == "string" and length > 0) or
                           ((.delta.reasoning? // "") | type == "string" and length > 0)
                       )
                   ' >/dev/null 2>&1 <<< "$event_data"; then
                    printf '%s\n' "$event_received_ns" > "$first_token_file"
                fi
            fi
        done
    pipeline_status=("${PIPESTATUS[@]}")
    curl_exit="${pipeline_status[0]}"
    set -e

    metrics=$(<"$curl_metrics_file")
    IFS=$'\t' read -r http_status time_start_transfer time_total <<< "$metrics"
    http_status="${http_status:-000}"
    time_start_transfer="${time_start_transfer:-0}"
    time_total="${time_total:-0}"

    end_ns=$(date +%s%N)
    finished_at=$(date -u +%Y-%m-%dT%H:%M:%S.%3NZ)
    elapsed_ms=$(( (end_ns - start_ns) / 1000000 ))
    started_offset_ms=$(( (start_ns - RUN_START_NS) / 1000000 ))
    finished_offset_ms=$(( (end_ns - RUN_START_NS) / 1000000 ))

    if (( curl_exit == 28 )); then
        timed_out=true
    fi

    if [[ "$STREAM" == false ]]; then
        stream_done=true
        if ! usage_json=$(jq -c '.usage // {}' "$response_file" 2>/dev/null); then
            usage_json='{}'
        fi
    else
        if grep -Eq '^data:[[:space:]]*\[DONE\][[:space:]]*$' "$response_file"; then
            stream_done=true
        fi

        if ! usage_json=$(
            sed -n 's/^data:[[:space:]]*//p' "$response_file" \
                | grep -v '^\[DONE\]$' \
                | jq -sc '[.[] | select(type == "object" and .usage != null) | .usage] | last // {}' \
                2>/dev/null
        ); then
            usage_json='{}'
        fi
    fi

    if [[ -s "$first_token_file" ]]; then
        first_token_ns=$(<"$first_token_file")
        if [[ "$first_token_ns" =~ ^[0-9]+$ ]] && (( first_token_ns >= start_ns )); then
            ttft_ms=$(( (first_token_ns - start_ns) / 1000000 ))
        fi
    fi

    completion_tokens=$(jq -r '.completion_tokens // empty' <<< "$usage_json" 2>/dev/null || true)
    if [[ "$ttft_ms" != null && "$completion_tokens" =~ ^[0-9]+$ ]] &&
       (( completion_tokens > 1 && elapsed_ms > ttft_ms )); then
        tpot_ms=$(awk \
            -v total_ms="$elapsed_ms" \
            -v first_ms="$ttft_ms" \
            -v tokens="$completion_tokens" \
            'BEGIN {printf "%.3f", (total_ms - first_ms) / (tokens - 1)}')
    fi

    if (( curl_exit == 0 )) &&
       [[ "$http_status" =~ ^2[0-9][0-9]$ ]] &&
       [[ "$stream_done" == true ]]; then
        success=true
    fi

    error_message=$(<"$error_file")
    artifact_file="$ARTIFACT_DIR/$(printf 'request-%06d.json' "$request_index")"
    artifact_tmp="$request_dir/artifact.json"

    jq -n \
        --slurpfile request_body "$REQUEST_BODY_FILE" \
        --rawfile response_body "$response_file" \
        --argjson request_index "$request_index" \
        --arg started_at "$started_at" \
        --arg finished_at "$finished_at" \
        --arg start_ns "$start_ns" \
        --arg end_ns "$end_ns" \
        --argjson scheduled_offset_ms "$((scheduled_offset_ns / 1000000))" \
        --argjson started_offset_ms "$started_offset_ms" \
        --argjson finished_offset_ms "$finished_offset_ms" \
        --argjson elapsed_ms "$elapsed_ms" \
        --argjson ttft_ms "$ttft_ms" \
        --argjson tpot_ms "$tpot_ms" \
        --arg http_status "$http_status" \
        --arg time_start_transfer "$time_start_transfer" \
        --arg time_total "$time_total" \
        --argjson curl_exit "$curl_exit" \
        --arg error "$error_message" \
        --argjson stream_done "$stream_done" \
        --argjson success "$success" \
        --argjson timed_out "$timed_out" \
        --argjson input_token_limit "$INPUT_TOKEN_LIMIT" \
        --argjson output_token_limit "$OUTPUT_TOKEN_LIMIT" \
        --argjson usage "$usage_json" \
        --arg url "$REQUEST_URL" \
        '{
            request_index: $request_index,
            success: $success,
            timed_out: $timed_out,
            stream_done: $stream_done,
            started_at: $started_at,
            finished_at: $finished_at,
            start_time_ns: $start_ns,
            end_time_ns: $end_ns,
            scheduled_offset_ms: $scheduled_offset_ms,
            started_offset_ms: $started_offset_ms,
            finished_offset_ms: $finished_offset_ms,
            schedule_lag_ms: ($started_offset_ms - $scheduled_offset_ms),
            duration_ms: $elapsed_ms,
            ttft_ms: $ttft_ms,
            tpot_ms: $tpot_ms,
            time_to_first_byte_ms: (($time_start_transfer | tonumber? // 0) * 1000),
            curl_total_ms: (($time_total | tonumber? // 0) * 1000),
            http_status: ($http_status | tonumber? // 0),
            curl_exit_code: $curl_exit,
            error: $error,
            request: {
                method: "POST",
                url: $url,
                headers: {
                    authorization: "[REDACTED]",
                    content_type: "application/json"
                },
                input_token_limit: $input_token_limit,
                output_token_limit: $output_token_limit,
                body: $request_body[0]
            },
            response_body: $response_body,
            usage: $usage,
            input_limit_exceeded: (
                ($usage.prompt_tokens? != null) and
                ($usage.prompt_tokens > $input_token_limit)
            )
        }' > "$artifact_tmp"

    mv "$artifact_tmp" "$artifact_file"

    live_metric_file="$LIVE_METRICS_DIR/$(printf 'request-%06d.json' "$request_index")"
    live_metric_tmp="$request_dir/live-metric.json"
    jq -n \
        --argjson request_index "$request_index" \
        --argjson success "$success" \
        --argjson timed_out "$timed_out" \
        --argjson ttft_ms "$ttft_ms" \
        --argjson tpot_ms "$tpot_ms" \
        '{
            request_index: $request_index,
            success: $success,
            timed_out: $timed_out,
            ttft_ms: $ttft_ms,
            tpot_ms: $tpot_ms
        }' > "$live_metric_tmp"
    mv "$live_metric_tmp" "$live_metric_file"

    return 0
}

render_progress() {
    local launched=0
    local completed=0
    local waiting=0
    local unsent=0
    local successful=0
    local failed=0
    local success_rate="0.00"
    local ttft_p50="-"
    local ttft_p90="-"
    local tpot_p50="-"
    local tpot_p90="-"
    local total_units
    local completed_units
    local launched_units
    local completed_full
    local completed_remainder
    local completed_cells
    local waiting_units
    local waiting_full
    local waiting_remainder
    local waiting_cells
    local unsent_cells
    local completed_bar
    local waiting_bar
    local unsent_bar
    local colored_bar
    local terminal_columns="${COLUMNS:-80}"
    local tty_size=''
    local color_green=''
    local color_yellow=''
    local color_gray=''
    local color_reset=''
    local stats
    local launched_files=()
    local progress_files=()
    local -a partial_blocks=('' '▏' '▎' '▍' '▌' '▋' '▊' '▉')
    local bar_width=73

    launched_files=("$LIVE_LAUNCHED_DIR"/request-*)
    launched=${#launched_files[@]}

    progress_files=("$LIVE_METRICS_DIR"/request-*.json)
    completed=${#progress_files[@]}
    progress_observed_completed=$completed
    if (( progress_stats_completed < 0 ||
          completed == NUM_REQUESTS ||
          (completed != progress_stats_completed && progress_frame % 4 == 0) )); then
        if (( completed > 0 )); then
            stats=$(jq -sr '
            def percentile($p):
                map(select(. != null))
                | sort as $values
                | if ($values | length) == 0 then null
                  else $values[((($p * ($values | length)) | ceil) - 1)]
                  end;

            {
                completed: length,
                successful: (map(select(.success == true)) | length),
                success_rate: (
                    if length == 0 then 0
                    else (map(select(.success == true)) | length) * 100 / length
                    end
                ),
                ttft_p50: (map(select(.success == true) | .ttft_ms) | percentile(0.50)),
                ttft_p90: (map(select(.success == true) | .ttft_ms) | percentile(0.90)),
                tpot_p50: (map(select(.success == true) | .tpot_ms) | percentile(0.50)),
                tpot_p90: (map(select(.success == true) | .tpot_ms) | percentile(0.90))
            }
            | [
                .completed,
                .successful,
                (.completed - .successful),
                (.success_rate | tostring),
                (.ttft_p50 // "-"),
                (.ttft_p90 // "-"),
                (.tpot_p50 // "-"),
                (.tpot_p90 // "-")
              ]
            | @tsv
            ' "${progress_files[@]}")

            IFS=$'\t' read -r completed progress_successful progress_failed progress_success_rate \
                progress_ttft_p50 progress_ttft_p90 progress_tpot_p50 progress_tpot_p90 <<< "$stats"
            progress_success_rate=$(awk -v value="$progress_success_rate" 'BEGIN {printf "%.2f", value}')
        else
            progress_successful=0
            progress_failed=0
            progress_success_rate="0.00"
            progress_ttft_p50="-"
            progress_ttft_p90="-"
            progress_tpot_p50="-"
            progress_tpot_p90="-"
        fi
        progress_stats_completed=$completed
    fi

    successful=$progress_successful
    failed=$progress_failed
    success_rate=$progress_success_rate
    ttft_p50=$progress_ttft_p50
    ttft_p90=$progress_ttft_p90
    tpot_p50=$progress_tpot_p50
    tpot_p90=$progress_tpot_p90

    if (( launched > NUM_REQUESTS )); then
        launched=$NUM_REQUESTS
    fi
    if (( completed > launched )); then
        completed=$launched
    fi
    waiting=$((launched - completed))
    unsent=$((NUM_REQUESTS - launched))

    if (( progress_bar_width == 0 )); then
        if [[ -t 1 ]]; then
            tty_size=$(stty size < /dev/tty 2>/dev/null || true)
            if [[ "$tty_size" =~ ^[0-9]+[[:space:]]+([0-9]+)$ ]]; then
                terminal_columns="${BASH_REMATCH[1]}"
            else
                terminal_columns=$(tput cols 2>/dev/null || printf '%s' "$terminal_columns")
            fi
        fi
        if [[ ! "$terminal_columns" =~ ^[1-9][0-9]*$ ]]; then
            terminal_columns=80
        fi

        # “进度 [”占 6 个显示列，右侧“]”占 1 列，进度条填满剩余宽度。
        progress_bar_width=$((terminal_columns - 7))
        if (( progress_bar_width < 1 )); then
            progress_bar_width=1
        fi
    fi
    bar_width=$progress_bar_width

    total_units=$((bar_width * 8))
    completed_units=$((completed * total_units / NUM_REQUESTS))
    launched_units=$((launched * total_units / NUM_REQUESTS))
    if (( completed == NUM_REQUESTS )); then
        completed_units=$total_units
    fi
    if (( launched == NUM_REQUESTS )); then
        launched_units=$total_units
    fi

    completed_full=$((completed_units / 8))
    completed_remainder=$((completed_units % 8))
    completed_cells=$completed_full
    printf -v completed_bar '%*s' "$completed_full" ''
    completed_bar="${completed_bar// /█}"
    if (( completed_remainder > 0 )); then
        completed_bar+="${partial_blocks[$completed_remainder]}"
        completed_cells=$((completed_cells + 1))
    fi

    # 完成段的半格已经占用一个终端字符；等待段从下一个字符继续。
    waiting_units=$((launched_units - completed_cells * 8))
    if (( waiting_units < 0 )); then
        waiting_units=0
    fi
    waiting_full=$((waiting_units / 8))
    waiting_remainder=$((waiting_units % 8))
    waiting_cells=$waiting_full
    printf -v waiting_bar '%*s' "$waiting_full" ''
    waiting_bar="${waiting_bar// /█}"
    if (( waiting_remainder > 0 )); then
        waiting_bar+="${partial_blocks[$waiting_remainder]}"
        waiting_cells=$((waiting_cells + 1))
    fi

    unsent_cells=$((bar_width - completed_cells - waiting_cells))
    if (( unsent_cells < 0 )); then
        unsent_cells=0
    fi
    printf -v unsent_bar '%*s' "$unsent_cells" ''
    unsent_bar="${unsent_bar// /-}"

    if [[ -t 1 && "${TERM:-dumb}" != "dumb" && -z "${NO_COLOR:-}" ]]; then
        color_green=$'\033[32m'
        color_yellow=$'\033[33m'
        color_gray=$'\033[90m'
        color_reset=$'\033[0m'
    fi
    printf -v colored_bar '%s%s%s%s%s%s%s' \
        "$color_green" "$completed_bar" \
        "$color_yellow" "$waiting_bar" \
        "$color_gray" "$unsent_bar" \
        "$color_reset"

    if [[ "${progress_rendered:-false}" == true && -t 1 ]]; then
        printf '\033[2A'
    fi
    if [[ -t 1 ]]; then
        printf '\033[2K\r进度 [%s]\n' "$colored_bar"
        printf '\033[2K\r指标 | 完成 %d | 等待 %d | 未发送 %d | 成功 %d | 失败 %d | 成功率 %s%% | TTFT P50/P90 %s/%s ms | TPOT P50/P90 %s/%s ms/token\n' \
            "$completed" "$waiting" "$unsent" \
            "$successful" "$failed" \
            "$success_rate" \
            "$ttft_p50" "$ttft_p90" \
            "$tpot_p50" "$tpot_p90"
    else
        printf '进度 [%s]\n' "$colored_bar"
        printf '指标 | 完成 %d | 等待 %d | 未发送 %d | 成功 %d | 失败 %d | 成功率 %s%% | TTFT P50/P90 %s/%s ms | TPOT P50/P90 %s/%s ms/token\n' \
            "$completed" "$waiting" "$unsent" \
            "$successful" "$failed" \
            "$success_rate" \
            "$ttft_p50" "$ttft_p90" \
            "$tpot_p50" "$tpot_p90"
    fi
    progress_rendered=true
}

progress_monitor() {
    local completed_count=0
    local progress_bar_width=0
    local progress_frame=0
    local progress_observed_completed=0
    local progress_rendered=false
    local progress_stats_completed=-1
    local progress_successful=0
    local progress_failed=0
    local progress_success_rate="0.00"
    local progress_ttft_p50="-"
    local progress_ttft_p90="-"
    local progress_tpot_p50="-"
    local progress_tpot_p90="-"

    while [[ ! -f "$MONITOR_STOP_FILE" ]]; do
        progress_frame=$((progress_frame + 1))
        render_progress
        completed_count=$progress_observed_completed
        if (( completed_count >= NUM_REQUESTS )); then
            break
        fi
        sleep "$PROGRESS_REFRESH_SECONDS"
    done
    render_progress
}

if (( SEND_DURATION > 0 )); then
    ARRIVAL_RPS=$(awk -v n="$NUM_REQUESTS" -v seconds="$SEND_DURATION" 'BEGIN {printf "%.3f", n / seconds}')
    ARRIVAL_RPM=$(awk -v n="$NUM_REQUESTS" -v seconds="$SEND_DURATION" 'BEGIN {printf "%.2f", n * 60 / seconds}')
else
    ARRIVAL_RPS="burst"
    ARRIVAL_RPM="burst"
fi

echo "开始压测"
echo "URL:             $REQUEST_URL"
echo "模型:            $MODEL"
echo "请求数:          $NUM_REQUESTS"
if (( SEND_DURATION > 0 )); then
    echo "并发策略:        开放式定时发送（-c $MAX_CONCURRENT 不限制在途数）"
else
    echo "最大并发:        $MAX_CONCURRENT"
fi
echo "发送窗口:        ${SEND_DURATION}s"
echo "目标发送速率:    ${ARRIVAL_RPS} RPS / ${ARRIVAL_RPM} RPM"
echo "单请求超时:      ${REQUEST_TIMEOUT}s"
echo "进度刷新间隔:    ${PROGRESS_REFRESH_MS}ms"
echo "输入 token 目标: $INPUT_TOKEN_LIMIT（近似值）"
echo "输出 token 上限: $OUTPUT_TOKEN_LIMIT"
echo "请求明细目录:    $ARTIFACT_DIR"
echo "汇总结果:        $OUTPUT_FILE"

if [[ -t 1 ]]; then
    printf '\033[?25l'
    CURSOR_HIDDEN=true
fi
progress_monitor &
MONITOR_PID=$!

SEND_START_NS=$(date +%s%N)
LAST_LAUNCH_NS=$SEND_START_NS
ACTIVE_JOBS=0
REQUEST_PIDS=()

for ((request_index = 1; request_index <= NUM_REQUESTS; request_index++)); do
    if (( SEND_DURATION > 0 )); then
        scheduled_offset_ns=$(( (request_index - 1) * SEND_DURATION * 1000000000 / NUM_REQUESTS ))
        target_ns=$((SEND_START_NS + scheduled_offset_ns))
        now_ns=$(date +%s%N)
        wait_ns=$((target_ns - now_ns))

        if (( wait_ns > 0 )); then
            printf -v sleep_seconds '%d.%09d' "$((wait_ns / 1000000000))" "$((wait_ns % 1000000000))"
            sleep "$sleep_seconds"
        fi
    else
        scheduled_offset_ns=0
    fi

    if (( SEND_DURATION == 0 )); then
        while (( ACTIVE_JOBS >= MAX_CONCURRENT )); do
            wait -n 2>/dev/null || true
            ACTIVE_JOBS=$((ACTIVE_JOBS - 1))
        done
    fi

    : > "$LIVE_LAUNCHED_DIR/$(printf 'request-%06d' "$request_index")"
    run_request "$request_index" "$scheduled_offset_ns" &
    REQUEST_PIDS+=("$!")
    ACTIVE_JOBS=$((ACTIVE_JOBS + 1))
    LAST_LAUNCH_NS=$(date +%s%N)
done

SEND_END_NS=$LAST_LAUNCH_NS
ACTUAL_SEND_DURATION_MS=$(( (SEND_END_NS - SEND_START_NS) / 1000000 ))
DRAIN_STARTED_AT=$(date -u +%Y-%m-%dT%H:%M:%S.%3NZ)
DRAIN_START_NS=$(date +%s%N)

for request_pid in "${REQUEST_PIDS[@]}"; do
    wait "$request_pid" 2>/dev/null || true
done
: > "$MONITOR_STOP_FILE"
wait "$MONITOR_PID" 2>/dev/null || true
if [[ "$CURSOR_HIDDEN" == true ]]; then
    printf '\033[?25h'
    CURSOR_HIDDEN=false
fi

DRAIN_END_NS=$(date +%s%N)
DRAIN_FINISHED_AT=$(date -u +%Y-%m-%dT%H:%M:%S.%3NZ)
DRAIN_DURATION_MS=$(( (DRAIN_END_NS - DRAIN_START_NS) / 1000000 ))
TOTAL_DURATION_MS=$(( (DRAIN_END_NS - RUN_START_NS) / 1000000 ))

mapfile -t ARTIFACT_FILES < <(find "$ARTIFACT_DIR" -maxdepth 1 -type f -name 'request-*.json' | sort)
if (( ${#ARTIFACT_FILES[@]} == 0 )); then
    echo "错误: 没有生成请求结果" >&2
    exit 1
fi

jq -s 'sort_by(.request_index)' "${ARTIFACT_FILES[@]}" > "$OUTPUT_FILE"

jq \
    --arg run_started_at "$RUN_STARTED_AT" \
    --arg drain_started_at "$DRAIN_STARTED_AT" \
    --arg drain_finished_at "$DRAIN_FINISHED_AT" \
    --arg url "$REQUEST_URL" \
    --arg model "$MODEL" \
    --arg results_file "$OUTPUT_FILE" \
    --arg artifact_dir "$ARTIFACT_DIR" \
    --arg concurrency_mode "$CONCURRENCY_MODE" \
    --argjson request_timeout_seconds "$REQUEST_TIMEOUT" \
    --argjson send_duration_seconds "$SEND_DURATION" \
    --argjson actual_send_duration_ms "$ACTUAL_SEND_DURATION_MS" \
    --argjson drain_duration_ms "$DRAIN_DURATION_MS" \
    --argjson total_duration_ms "$TOTAL_DURATION_MS" \
    --argjson requested "$NUM_REQUESTS" \
    --argjson concurrency "$MAX_CONCURRENT" \
    --argjson effective_concurrency_limit "$EFFECTIVE_CONCURRENCY_LIMIT" \
    --argjson input_token_limit "$INPUT_TOKEN_LIMIT" \
    --argjson output_token_limit "$OUTPUT_TOKEN_LIMIT" \
    '
    . as $requests
    | {
        run_started_at: $run_started_at,
        drain_started_at: $drain_started_at,
        drain_finished_at: $drain_finished_at,
        url: $url,
        model: $model,
        results_file: $results_file,
        artifact_dir: $artifact_dir,
        requested: $requested,
        recorded: ($requests | length),
        concurrency: $concurrency,
        concurrency_mode: $concurrency_mode,
        effective_concurrency_limit: (
            if $effective_concurrency_limit == 0 then null
            else $effective_concurrency_limit
            end
        ),
        request_timeout_seconds: $request_timeout_seconds,
        send_duration_seconds: $send_duration_seconds,
        actual_send_duration_ms: $actual_send_duration_ms,
        drain_duration_ms: $drain_duration_ms,
        total_duration_ms: $total_duration_ms,
        input_token_limit: $input_token_limit,
        output_token_limit: $output_token_limit,
        successful: ($requests | map(select(.success == true)) | length),
        failed: ($requests | map(select(.success != true)) | length),
        success_rate_percent: (
            if ($requests | length) == 0 then 0
            else (($requests | map(select(.success == true)) | length) * 100 / ($requests | length))
            end
        ),
        timed_out: ($requests | map(select(.timed_out == true)) | length),
        incomplete_streams: ($requests | map(select(.stream_done != true)) | length),
        input_limit_exceeded: ($requests | map(select(.input_limit_exceeded == true)) | length),
        ttft_p50_ms: (
            [$requests[] | select(.success == true) | .ttft_ms | select(. != null)] | sort
            | if length == 0 then null else .[((length * 0.50 | ceil) - 1)] end
        ),
        ttft_p90_ms: (
            [$requests[] | select(.success == true) | .ttft_ms | select(. != null)] | sort
            | if length == 0 then null else .[((length * 0.90 | ceil) - 1)] end
        ),
        tpot_p50_ms: (
            [$requests[] | select(.success == true) | .tpot_ms | select(. != null)] | sort
            | if length == 0 then null else .[((length * 0.50 | ceil) - 1)] end
        ),
        tpot_p90_ms: (
            [$requests[] | select(.success == true) | .tpot_ms | select(. != null)] | sort
            | if length == 0 then null else .[((length * 0.90 | ceil) - 1)] end
        ),
        maximum_schedule_lag_ms: ($requests | map(.schedule_lag_ms) | max // 0),
        http_statuses: (
            $requests
            | group_by(.http_status)
            | map({status: .[0].http_status, count: length})
        ),
        completion_timeline: (
            $requests
            | map({request_index, finished_offset_ms, success, timed_out, http_status})
            | sort_by(.finished_offset_ms)
        )
    }' "$OUTPUT_FILE" > "$SUMMARY_FILE"

echo "压测完成"
echo "实际发送耗时: ${ACTUAL_SEND_DURATION_MS}ms"
echo "发送后排空耗时: ${DRAIN_DURATION_MS}ms"
echo "测试总耗时: ${TOTAL_DURATION_MS}ms"
echo "请求明细: $ARTIFACT_DIR"
echo "结果数组: $OUTPUT_FILE"
echo "汇总文件: $SUMMARY_FILE"
