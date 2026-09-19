package load

import (
	"math"
	"sort"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
)

type Metrics struct {
	Completed          uint64  `json:"completed"`
	Succeeded          uint64  `json:"succeeded"`
	Failed             uint64  `json:"failed"`
	TimedOut           uint64  `json:"timed_out"`
	SuccessRatePercent float64 `json:"success_rate_percent"`
	// OfferedQPS is the scheduler demand (launched plus locally rejected)
	// divided by the send window. LaunchedQPS is the demand admitted by the
	// client. SuccessfulRequestQPS is aligned with AIPerf request throughput:
	// only valid responses divided by the full benchmark window.
	OfferedQPS           float64 `json:"offered_qps,omitempty"`
	LaunchedQPS          float64 `json:"launched_qps,omitempty"`
	CompletedQPS         float64 `json:"completed_qps,omitempty"`
	SuccessfulRequestQPS float64 `json:"successful_request_qps,omitempty"`
	// RequestQPS is the all-outcomes completion rate over the full window.
	RequestQPS                float64 `json:"request_qps"`
	RPM                       float64 `json:"rpm"`
	InputTPM                  float64 `json:"input_tpm"`
	OutputTPM                 float64 `json:"output_tpm"`
	TotalTPM                  float64 `json:"total_tpm"`
	GenerationTPS             float64 `json:"generation_tps"`
	TTFTSamples               uint64  `json:"ttft_samples"`
	TTFTP50                   float64 `json:"ttft_p50_ms"`
	TTFTP90                   float64 `json:"ttft_p90_ms"`
	TTFTP95                   float64 `json:"ttft_p95_ms"`
	TTFTP99                   float64 `json:"ttft_p99_ms"`
	TTFTAverage               float64 `json:"ttft_average_ms"`
	TTFBSamples               uint64  `json:"ttfb_samples"`
	TTFBP50                   float64 `json:"ttfb_p50_ms"`
	TTFBP95                   float64 `json:"ttfb_p95_ms"`
	TTFBP99                   float64 `json:"ttfb_p99_ms"`
	TTFBAverage               float64 `json:"ttfb_average_ms"`
	TTFTAnySamples            uint64  `json:"ttft_any_samples"`
	TTFTAnyP50                float64 `json:"ttft_any_p50_ms"`
	TTFTAnyP95                float64 `json:"ttft_any_p95_ms"`
	TTFTAnyP99                float64 `json:"ttft_any_p99_ms"`
	TTFTAnyAverage            float64 `json:"ttft_any_average_ms"`
	TTFTVisibleSamples        uint64  `json:"ttft_visible_samples"`
	TTFTVisibleP50            float64 `json:"ttft_visible_p50_ms"`
	TTFTVisibleP95            float64 `json:"ttft_visible_p95_ms"`
	TTFTVisibleP99            float64 `json:"ttft_visible_p99_ms"`
	TTFTVisibleAverage        float64 `json:"ttft_visible_average_ms"`
	TTSTSamples               uint64  `json:"ttst_samples"`
	TTSTP50                   float64 `json:"ttst_p50_ms"`
	TTSTP95                   float64 `json:"ttst_p95_ms"`
	TTSTP99                   float64 `json:"ttst_p99_ms"`
	TTSTAverage               float64 `json:"ttst_average_ms"`
	ObservedICLSamples        uint64  `json:"observed_icl_samples"`
	ObservedICLP50            float64 `json:"observed_icl_p50_ms"`
	ObservedICLP95            float64 `json:"observed_icl_p95_ms"`
	ObservedICLP99            float64 `json:"observed_icl_p99_ms"`
	ObservedICLAverage        float64 `json:"observed_icl_average_ms"`
	SemanticChunkCountSamples uint64  `json:"semantic_chunk_count_samples"`
	SemanticChunkCountP50     float64 `json:"semantic_chunk_count_p50"`
	SemanticChunkCountP95     float64 `json:"semantic_chunk_count_p95"`
	SemanticChunkCountP99     float64 `json:"semantic_chunk_count_p99"`
	SemanticChunkCountAverage float64 `json:"semantic_chunk_count_average"`
	TPOTP50                   float64 `json:"tpot_p50_ms"`
	TPOTP90                   float64 `json:"tpot_p90_ms"`
	TPOTP95                   float64 `json:"tpot_p95_ms"`
	TPOTP99                   float64 `json:"tpot_p99_ms"`
	TPOTAverage               float64 `json:"tpot_average_ms"`
	E2EP50                    float64 `json:"e2e_p50_ms"`
	E2EP90                    float64 `json:"e2e_p90_ms"`
	E2EP95                    float64 `json:"e2e_p95_ms"`
	E2EP99                    float64 `json:"e2e_p99_ms"`
	E2EAverage                float64 `json:"e2e_average_ms"`
	ScheduleLagP50            float64 `json:"schedule_lag_p50_ms"`
	ScheduleLagP90            float64 `json:"schedule_lag_p90_ms,omitempty"`
	ScheduleLagP95            float64 `json:"schedule_lag_p95_ms"`
	ScheduleLagP99            float64 `json:"schedule_lag_p99_ms,omitempty"`
	ScheduleLagAverage        float64 `json:"schedule_lag_average_ms"`
	PromptTokens              uint64  `json:"prompt_tokens"`
	CompletionTokens          uint64  `json:"completion_tokens"`
	CachedTokens              uint64  `json:"cached_tokens"`
	CacheRatePercent          float64 `json:"cache_rate_percent"`
}

func ComputeMetrics(observations []Observation, elapsed time.Duration) Metrics {
	return ComputeMetricsWithProgress(observations, Progress{
		Offered:       uint64(len(observations)),
		Launched:      uint64(len(observations)),
		SendDuration:  elapsed,
		TotalDuration: elapsed,
	})
}

func ComputeMetricsWithProgress(observations []Observation, progress Progress) Metrics {
	metrics := Metrics{Completed: uint64(len(observations))}
	ttfts := make([]float64, 0, len(observations))
	ttfbs := make([]float64, 0, len(observations))
	ttftVisibles := make([]float64, 0, len(observations))
	ttsts := make([]float64, 0, len(observations))
	observedICLs := make([]float64, 0, len(observations))
	semanticChunkCounts := make([]float64, 0, len(observations))
	tpots := make([]float64, 0, len(observations))
	e2es := make([]float64, 0, len(observations))
	lags := make([]float64, 0, len(observations))

	for _, observation := range observations {
		observation = normalizeObservationTimings(observation)
		if observation.TimedOut {
			metrics.TimedOut++
		}
		lags = append(lags, milliseconds(observation.ScheduleLag))
		if !observation.Success {
			continue
		}
		metrics.Succeeded++
		metrics.PromptTokens += observation.PromptTokens
		metrics.CompletionTokens += observation.CompletionTokens
		metrics.CachedTokens += observation.CachedTokens
		e2es = append(e2es, milliseconds(observation.E2E))
		if observation.TTFB > 0 {
			ttfbs = append(ttfbs, milliseconds(observation.TTFB))
		}
		if observation.TTFTAny > 0 {
			ttfts = append(ttfts, milliseconds(observation.TTFTAny))
		}
		if observation.TTFTVisible > 0 {
			ttftVisibles = append(ttftVisibles, milliseconds(observation.TTFTVisible))
		}
		if observation.SemanticChunkCount >= 2 && observation.TTFTAny > 0 && observation.TTST > 0 {
			ttsts = append(ttsts, milliseconds(observation.TTST))
			observedICLs = append(observedICLs, milliseconds(observation.ObservedICL))
		}
		if observation.Streaming {
			semanticChunkCounts = append(semanticChunkCounts, float64(observation.SemanticChunkCount))
		}
		if observation.TTFTAny > 0 && observation.E2E > observation.TTFTAny && observation.CompletionTokens > 1 {
			intervals := float64(observation.CompletionTokens - 1)
			tpots = append(tpots, milliseconds(observation.E2E-observation.TTFTAny)/intervals)
		}
	}
	metrics.Failed = metrics.Completed - metrics.Succeeded
	if metrics.Completed > 0 {
		metrics.SuccessRatePercent = float64(metrics.Succeeded) / float64(metrics.Completed) * 100
	}
	seconds := progress.TotalDuration.Seconds()
	if seconds > 0 {
		metrics.CompletedQPS = float64(progress.Launched) / seconds
		metrics.SuccessfulRequestQPS = float64(metrics.Succeeded) / seconds
		metrics.RequestQPS = float64(metrics.Completed) / seconds
		metrics.RPM = metrics.RequestQPS * 60
		metrics.InputTPM = float64(metrics.PromptTokens) / seconds * 60
		metrics.OutputTPM = float64(metrics.CompletionTokens) / seconds * 60
		metrics.TotalTPM = metrics.InputTPM + metrics.OutputTPM
		metrics.GenerationTPS = float64(metrics.CompletionTokens) / seconds
	}
	sendSeconds := progress.SendDuration.Seconds()
	if sendSeconds > 0 {
		offered := progress.Offered
		if offered == 0 {
			offered = progress.Launched + progress.Rejected
		}
		metrics.OfferedQPS = float64(offered) / sendSeconds
		metrics.LaunchedQPS = float64(progress.Launched) / sendSeconds
	}
	metrics.TTFTSamples = uint64(len(ttfts))
	metrics.TTFTP50, metrics.TTFTP90, metrics.TTFTP95, metrics.TTFTP99, metrics.TTFTAverage = summarize(ttfts)
	metrics.TTFTAnySamples = metrics.TTFTSamples
	metrics.TTFTAnyP50 = metrics.TTFTP50
	metrics.TTFTAnyP95 = metrics.TTFTP95
	metrics.TTFTAnyP99 = metrics.TTFTP99
	metrics.TTFTAnyAverage = metrics.TTFTAverage
	metrics.TTFBSamples = uint64(len(ttfbs))
	metrics.TTFBP50, _, metrics.TTFBP95, metrics.TTFBP99, metrics.TTFBAverage = summarize(ttfbs)
	metrics.TTFTVisibleSamples = uint64(len(ttftVisibles))
	metrics.TTFTVisibleP50, _, metrics.TTFTVisibleP95, metrics.TTFTVisibleP99, metrics.TTFTVisibleAverage = summarize(ttftVisibles)
	metrics.TTSTSamples = uint64(len(ttsts))
	metrics.TTSTP50, _, metrics.TTSTP95, metrics.TTSTP99, metrics.TTSTAverage = summarize(ttsts)
	metrics.ObservedICLSamples = uint64(len(observedICLs))
	metrics.ObservedICLP50, _, metrics.ObservedICLP95, metrics.ObservedICLP99, metrics.ObservedICLAverage = summarize(observedICLs)
	metrics.SemanticChunkCountSamples = uint64(len(semanticChunkCounts))
	metrics.SemanticChunkCountP50, _, metrics.SemanticChunkCountP95, metrics.SemanticChunkCountP99, metrics.SemanticChunkCountAverage = summarize(semanticChunkCounts)
	metrics.TPOTP50, metrics.TPOTP90, metrics.TPOTP95, metrics.TPOTP99, metrics.TPOTAverage = summarize(tpots)
	metrics.E2EP50, metrics.E2EP90, metrics.E2EP95, metrics.E2EP99, metrics.E2EAverage = summarize(e2es)
	metrics.ScheduleLagP50, metrics.ScheduleLagP90, metrics.ScheduleLagP95, metrics.ScheduleLagP99, metrics.ScheduleLagAverage = summarize(lags)
	if metrics.PromptTokens > 0 {
		metrics.CacheRatePercent = float64(metrics.CachedTokens) / float64(metrics.PromptTokens) * 100
	}
	return metrics
}

func normalizeObservationTimings(observation Observation) Observation {
	observation.TTFB = nonNegativeDuration(observation.TTFB)
	observation.TTFT = nonNegativeDuration(observation.TTFT)
	observation.TTFTAny = nonNegativeDuration(observation.TTFTAny)
	observation.TTFTVisible = nonNegativeDuration(observation.TTFTVisible)
	observation.TTST = nonNegativeDuration(observation.TTST)
	observation.ObservedICL = nonNegativeDuration(observation.ObservedICL)
	if observation.TTFTAny == 0 && observation.TTFT > 0 {
		observation.TTFTAny = observation.TTFT
	}
	observation.TTFT = observation.TTFTAny

	if observation.E2E > 0 {
		if observation.TTFB > observation.E2E {
			observation.TTFB = 0
		}
		if observation.TTFTAny > observation.E2E {
			observation.TTFTAny = 0
			observation.TTFT = 0
		}
		if observation.TTFTVisible > observation.E2E {
			observation.TTFTVisible = 0
		}
		if observation.TTST > observation.E2E {
			observation.TTST = 0
		}
	}
	if observation.TTFB > 0 && observation.TTFTAny > 0 && observation.TTFB > observation.TTFTAny {
		observation.TTFB = 0
	}
	if observation.TTFTVisible > 0 && (observation.TTFTAny == 0 || observation.TTFTVisible < observation.TTFTAny) {
		observation.TTFTVisible = 0
	}
	if !observation.Streaming {
		observation.TTST = 0
		observation.ObservedICL = 0
		observation.SemanticChunkCount = 0
		return observation
	}
	if observation.TTFTAny == 0 {
		observation.TTFTVisible = 0
		observation.TTST = 0
		observation.ObservedICL = 0
		observation.SemanticChunkCount = 0
		return observation
	}
	if observation.SemanticChunkCount == 0 {
		observation.SemanticChunkCount = 1
	}
	if observation.SemanticChunkCount == 1 {
		observation.TTST = 0
		observation.ObservedICL = 0
		if observation.TTFTVisible > observation.TTFTAny {
			observation.TTFTVisible = 0
		}
		return observation
	}
	if observation.TTST == 0 || observation.TTST < observation.TTFTAny {
		observation.TTST = 0
		observation.ObservedICL = 0
		observation.SemanticChunkCount = 1
		if observation.TTFTVisible > observation.TTFTAny {
			observation.TTFTVisible = 0
		}
	}
	return observation
}

func nonNegativeDuration(value time.Duration) time.Duration {
	if value < 0 {
		return 0
	}
	return value
}

func ComputeMetricsWithProfile(observations []Observation, progress Progress, profile domain.LoadProfile) Metrics {
	return ComputeMetricsWithArrival(observations, progress, profile, ArrivalConstant)
}

func ComputeMetricsWithArrival(observations []Observation, progress Progress, profile domain.LoadProfile, arrival ArrivalPattern) Metrics {
	metrics := ComputeMetricsWithProgress(observations, progress)
	if profile.Mode != domain.LoadOpenLoop || progress.Stopped || profile.RatePerSecond <= 0 {
		return metrics
	}
	offered := progress.Offered
	if offered == 0 {
		offered = progress.Launched + progress.Rejected
	}
	rateWindowSeconds := progress.SendDuration.Seconds()
	countLimitReached := profile.RequestCount > 0 && offered >= profile.RequestCount
	useNominalCountWindow := arrival != ArrivalPoisson || offered == 1
	if countLimitReached && useNominalCountWindow {
		minimumScheduleWindow := float64(offered) / profile.RatePerSecond
		if rateWindowSeconds < minimumScheduleWindow {
			rateWindowSeconds = minimumScheduleWindow
		}
	}
	if rateWindowSeconds > 0 {
		metrics.OfferedQPS = float64(offered) / rateWindowSeconds
		metrics.LaunchedQPS = float64(progress.Launched) / rateWindowSeconds
	}
	return metrics
}

func summarize(values []float64) (p50, p90, p95, p99, average float64) {
	if len(values) == 0 {
		return 0, 0, 0, 0, 0
	}
	sort.Float64s(values)
	for _, value := range values {
		average += value
	}
	return percentile(values, 0.5), percentile(values, 0.9), percentile(values, 0.95), percentile(values, 0.99), average / float64(len(values))
}

func percentile(sortedValues []float64, quantile float64) float64 {
	if len(sortedValues) == 0 {
		return 0
	}
	position := float64(len(sortedValues)-1) * quantile
	lower := int(math.Floor(position))
	upper := int(math.Ceil(position))
	if lower == upper {
		return sortedValues[lower]
	}
	weight := position - float64(lower)
	return sortedValues[lower]*(1-weight) + sortedValues[upper]*weight
}

func milliseconds(duration time.Duration) float64 {
	return float64(duration) / float64(time.Millisecond)
}
