package quicktest

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
	"sync"

	"github.com/894x/llm-test-studio/internal/execution/load"
)

type PerformanceWorkloadMode string

const (
	PerformanceWorkloadFixed  PerformanceWorkloadMode = "fixed"
	PerformanceWorkloadNormal PerformanceWorkloadMode = "normal"

	maxPerformanceInputTokensInFlight uint64 = 2_000_000
)

var sharedPerformanceInputTokenBudget = newPerformanceTokenBudget(maxPerformanceInputTokensInFlight)

type performanceWorkloadTarget struct {
	InputTokens  uint32
	OutputTokens uint32
}

type performanceWorkload struct {
	profile PerformanceProfile
}

func normalizedWorkloadMode(mode PerformanceWorkloadMode) PerformanceWorkloadMode {
	if mode == "" {
		return PerformanceWorkloadFixed
	}
	return mode
}

func normalizedArrivalPattern(pattern load.ArrivalPattern) load.ArrivalPattern {
	if pattern == "" {
		return load.ArrivalConstant
	}
	return pattern
}

func newPerformanceWorkload(profile PerformanceProfile) (*performanceWorkload, error) {
	if normalizedWorkloadMode(profile.WorkloadMode) != PerformanceWorkloadNormal ||
		profile.RandomSeed == 0 ||
		profile.InputTokens == 0 || profile.OutputTokens == 0 ||
		profile.InputTokens > MaxPerformanceInputTokens || profile.OutputTokens > MaxPerformanceOutputTokens ||
		profile.InputTokensStdDev > profile.InputTokens || profile.OutputTokensStdDev > profile.OutputTokens ||
		profile.SharedPrefixTokens >= profile.InputTokens || profile.SharedPrefixTokens >= MaxPerformanceInputTokens {
		return nil, errors.New("invalid normal performance workload")
	}
	profile.WorkloadMode = PerformanceWorkloadNormal
	return &performanceWorkload{profile: profile}, nil
}

func (workload *performanceWorkload) target(index uint64) performanceWorkloadTarget {
	inputMinimum := uint32(1)
	if workload.profile.SharedPrefixTokens > 0 {
		inputMinimum = workload.profile.SharedPrefixTokens + 1
	}
	return performanceWorkloadTarget{
		InputTokens: stableNormalTarget(
			workload.profile.InputTokens,
			workload.profile.InputTokensStdDev,
			inputMinimum,
			MaxPerformanceInputTokens,
			workload.profile.RandomSeed,
			index,
			0,
		),
		OutputTokens: stableNormalTarget(
			workload.profile.OutputTokens,
			workload.profile.OutputTokensStdDev,
			1,
			MaxPerformanceOutputTokens,
			workload.profile.RandomSeed,
			index,
			1,
		),
	}
}

func (workload *performanceWorkload) prompt(index uint64, target uint32) string {
	if target == 0 {
		return ""
	}
	sharedCount := min(workload.profile.SharedPrefixTokens, target)
	uniqueWord := "r" + strconv.FormatUint(index, 36)
	estimatedBytes := uint64(sharedCount)*2 + uint64(target-sharedCount)*uint64(len(uniqueWord)+1)
	if estimatedBytes > 0 {
		estimatedBytes--
	}
	var builder strings.Builder
	builder.Grow(int(estimatedBytes))
	for position := uint32(0); position < target; position++ {
		if position > 0 {
			builder.WriteByte(' ')
		}
		if position < sharedCount {
			builder.WriteByte('s')
		} else {
			builder.WriteString(uniqueWord)
		}
	}
	return builder.String()
}

func (workload *performanceWorkload) requestBodyForTarget(index uint64, target performanceWorkloadTarget) (json.RawMessage, error) {
	body, err := json.Marshal(map[string]any{
		"messages":   []map[string]string{{"role": "user", "content": workload.prompt(index, target.InputTokens)}},
		"max_tokens": target.OutputTokens,
		"stream":     true,
	})
	return body, err
}

type performanceTokenBudget struct {
	mu       sync.Mutex
	capacity uint64
	used     uint64
	changed  chan struct{}
}

func newPerformanceTokenBudget(capacity uint64) *performanceTokenBudget {
	if capacity == 0 {
		panic("performance token budget requires positive capacity")
	}
	return &performanceTokenBudget{capacity: capacity, changed: make(chan struct{})}
}

func (budget *performanceTokenBudget) acquire(ctx context.Context, requested uint32) (func(), error) {
	if ctx == nil {
		return nil, errors.New("performance token budget context is required")
	}
	weight := min(uint64(requested), budget.capacity)
	if weight == 0 {
		return func() {}, nil
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		budget.mu.Lock()
		if budget.used <= budget.capacity-weight {
			budget.used += weight
			budget.mu.Unlock()
			var once sync.Once
			return func() {
				once.Do(func() {
					budget.mu.Lock()
					budget.used -= weight
					close(budget.changed)
					budget.changed = make(chan struct{})
					budget.mu.Unlock()
				})
			}, nil
		}
		changed := budget.changed
		budget.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-changed:
		}
	}
}

// stableNormalTarget maps (seed, request index, metric stream) to a clamped
// rounded normal sample. It uses SplitMix64 finalization plus Box-Muller, so
// output is independent from goroutine ordering and math/rand implementation
// changes. Stream 0 is input size and stream 1 is output size.
func stableNormalTarget(mean, stddev, minimum, maximum uint32, seed uint32, index uint64, stream uint64) uint32 {
	if stddev == 0 {
		return clampUint32(mean, minimum, maximum)
	}
	key := uint64(seed)<<32 ^ index*0x9e3779b97f4a7c15 ^ stream*0xd1b54a32d192ed03
	u1 := stableUniform(key ^ 0xa0761d6478bd642f)
	u2 := stableUniform(key ^ 0xe7037ed1a0b428db)
	normal := math.Sqrt(-2*math.Log(u1)) * math.Cos(2*math.Pi*u2)
	value := math.Round(float64(mean) + float64(stddev)*normal)
	if value <= float64(minimum) {
		return minimum
	}
	if value >= float64(maximum) {
		return maximum
	}
	return uint32(value)
}

func stableUniform(value uint64) float64 {
	value += 0x9e3779b97f4a7c15
	value = (value ^ (value >> 30)) * 0xbf58476d1ce4e5b9
	value = (value ^ (value >> 27)) * 0x94d049bb133111eb
	value ^= value >> 31
	return (float64(value>>11) + 0.5) / (1 << 53)
}

func clampUint32(value, minimum, maximum uint32) uint32 {
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}
