package quicktest

import (
	"math"
	"sort"
	"time"

	"github.com/894x/llm-test-studio/internal/execution/load"
)

type performanceTimeSliceAccumulator struct {
	slice PerformanceTimeSlice
	ttft  []float64
	tpot  []float64
	e2e   []float64
}

func buildPerformanceTimeSlices(observations []load.Observation, totalDuration, sliceDuration time.Duration) []PerformanceTimeSlice {
	if len(observations) == 0 || totalDuration <= 0 || sliceDuration <= 0 {
		return nil
	}
	byIndex := make(map[uint64]*performanceTimeSliceAccumulator)
	get := func(offset time.Duration) *performanceTimeSliceAccumulator {
		index := performanceSliceIndex(offset, totalDuration, sliceDuration)
		if current := byIndex[index]; current != nil {
			return current
		}
		start := time.Duration(index) * sliceDuration
		nominalEnd := start + sliceDuration
		end := nominalEnd
		if end > totalDuration {
			end = totalDuration
		}
		current := &performanceTimeSliceAccumulator{slice: PerformanceTimeSlice{
			SliceIndex: index,
			StartMS:    durationMilliseconds(start),
			EndMS:      durationMilliseconds(end),
			Partial:    end < nominalEnd,
		}}
		byIndex[index] = current
		return current
	}

	for _, observation := range observations {
		get(observation.ScheduledOffset).slice.Offered++
		rejected := observation.ErrorCode == load.ErrorSchedulerOverload
		if !rejected {
			get(observation.StartedOffset).slice.Launched++
		}
		completed := get(observation.FinishedOffset)
		completed.slice.Completed++
		if observation.Success {
			completed.slice.Succeeded++
			completed.slice.PromptTokens += observation.PromptTokens
			completed.slice.CompletionTokens += observation.CompletionTokens
			completed.slice.CachedTokens += observation.CachedTokens

			latency := get(observation.StartedOffset)
			if observation.TTFT > 0 {
				latency.ttft = append(latency.ttft, durationMilliseconds(observation.TTFT))
			}
			if observation.E2E > 0 {
				latency.e2e = append(latency.e2e, durationMilliseconds(observation.E2E))
			}
			if tpot := performanceTPOTMilliseconds(
				durationMilliseconds(observation.TTFT),
				durationMilliseconds(observation.E2E),
				observation.CompletionTokens,
			); tpot > 0 {
				latency.tpot = append(latency.tpot, tpot)
			}
		} else {
			completed.slice.Failed++
			if rejected {
				completed.slice.Rejected++
			}
		}
	}
	if totalDuration%sliceDuration != 0 {
		get(totalDuration - 1)
	}

	indices := make([]uint64, 0, len(byIndex))
	for index := range byIndex {
		indices = append(indices, index)
	}
	sort.Slice(indices, func(left, right int) bool { return indices[left] < indices[right] })
	result := make([]PerformanceTimeSlice, 0, len(indices))
	for _, index := range indices {
		current := byIndex[index]
		current.slice.TTFT = performanceLatencySlice(current.ttft)
		current.slice.TPOT = performanceLatencySlice(current.tpot)
		current.slice.E2E = performanceLatencySlice(current.e2e)
		result = append(result, current.slice)
	}
	return result
}

func buildPerformanceTimeSlicesFromSamples(samples []PerformanceSample, totalDurationMS, sliceDurationMS float64) []PerformanceTimeSlice {
	if len(samples) == 0 || totalDurationMS <= 0 || sliceDurationMS <= 0 {
		return nil
	}
	byIndex := make(map[uint64]*performanceTimeSliceAccumulator)
	get := func(offsetMS float64) *performanceTimeSliceAccumulator {
		index := performanceSliceIndexMilliseconds(offsetMS, totalDurationMS, sliceDurationMS)
		if current := byIndex[index]; current != nil {
			return current
		}
		start := float64(index) * sliceDurationMS
		nominalEnd := start + sliceDurationMS
		end := math.Min(nominalEnd, totalDurationMS)
		current := &performanceTimeSliceAccumulator{slice: PerformanceTimeSlice{
			SliceIndex: index,
			StartMS:    start,
			EndMS:      end,
			Partial:    end < nominalEnd,
		}}
		byIndex[index] = current
		return current
	}

	for _, sample := range samples {
		get(sample.ScheduledOffsetMS).slice.Offered++
		rejected := sample.ErrorCode == load.ErrorSchedulerOverload
		if !rejected {
			get(sample.StartedOffsetMS).slice.Launched++
		}
		completed := get(sample.FinishedOffsetMS)
		completed.slice.Completed++
		if sample.Success {
			completed.slice.Succeeded++
			completed.slice.PromptTokens += sample.PromptTokens
			completed.slice.CompletionTokens += sample.CompletionTokens
			completed.slice.CachedTokens += sample.CachedTokens

			latency := get(sample.StartedOffsetMS)
			if sample.TTFTMS > 0 {
				latency.ttft = append(latency.ttft, sample.TTFTMS)
			}
			if tpot := performanceTPOTMilliseconds(sample.TTFTMS, sample.E2EMS, sample.CompletionTokens); tpot > 0 {
				latency.tpot = append(latency.tpot, tpot)
			}
			if sample.E2EMS > 0 {
				latency.e2e = append(latency.e2e, sample.E2EMS)
			}
		} else {
			completed.slice.Failed++
			if rejected {
				completed.slice.Rejected++
			}
		}
	}
	if math.Mod(totalDurationMS, sliceDurationMS) != 0 {
		get(math.Nextafter(totalDurationMS, math.Inf(-1)))
	}

	indices := make([]uint64, 0, len(byIndex))
	for index := range byIndex {
		indices = append(indices, index)
	}
	sort.Slice(indices, func(left, right int) bool { return indices[left] < indices[right] })
	result := make([]PerformanceTimeSlice, 0, len(indices))
	for _, index := range indices {
		current := byIndex[index]
		current.slice.TTFT = performanceLatencySlice(current.ttft)
		current.slice.TPOT = performanceLatencySlice(current.tpot)
		current.slice.E2E = performanceLatencySlice(current.e2e)
		result = append(result, current.slice)
	}
	return result
}

func performanceTPOTMilliseconds(ttftMS, e2eMS float64, completionTokens uint64) float64 {
	if ttftMS <= 0 || e2eMS <= ttftMS || completionTokens <= 1 {
		return 0
	}
	return (e2eMS - ttftMS) / float64(completionTokens-1)
}

func performanceSliceIndex(offset, totalDuration, sliceDuration time.Duration) uint64 {
	if offset < 0 {
		offset = 0
	}
	if offset >= totalDuration {
		offset = totalDuration - 1
	}
	return uint64(offset / sliceDuration)
}

func performanceSliceIndexMilliseconds(offset, totalDuration, sliceDuration float64) uint64 {
	if offset < 0 {
		offset = 0
	}
	if offset >= totalDuration {
		offset = math.Nextafter(totalDuration, math.Inf(-1))
	}
	return uint64(math.Floor(offset / sliceDuration))
}

func performanceLatencySlice(values []float64) PerformanceLatencySlice {
	if len(values) == 0 {
		return PerformanceLatencySlice{}
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	return PerformanceLatencySlice{
		Count: uint64(len(sorted)),
		P50MS: performancePercentile(sorted, 0.50),
		P95MS: performancePercentile(sorted, 0.95),
		P99MS: performancePercentile(sorted, 0.99),
	}
}

func performancePercentile(sorted []float64, quantile float64) float64 {
	position := float64(len(sorted)-1) * quantile
	lower := int(math.Floor(position))
	upper := int(math.Ceil(position))
	if lower == upper {
		return sorted[lower]
	}
	weight := position - float64(lower)
	return sorted[lower]*(1-weight) + sorted[upper]*weight
}
