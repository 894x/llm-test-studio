package caseimport

import (
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/894x/llm-test/internal/domain"
)

type legacyLoadProfile struct {
	Name                 string                    `json:"name"`
	Model                string                    `json:"model"`
	Protocol             string                    `json:"protocol"`
	RequestPath          string                    `json:"request_path"`
	InputTokensTarget    int                       `json:"input_tokens_target"`
	MaxOutputTokens      int                       `json:"max_output_tokens"`
	TurnsPerSession      int                       `json:"turns_per_session"`
	Sessions             int                       `json:"sessions"`
	TotalRequests        int                       `json:"total_requests"`
	Concurrency          int                       `json:"concurrency"`
	ArrivalRatePerSecond legacyArrivalRate         `json:"arrival_rate_per_second"`
	RampSeconds          int                       `json:"ramp_seconds"`
	ReferenceResult      legacyLoadReferenceResult `json:"reference_result"`
}

type legacyArrivalRate struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

type legacyLoadReferenceResult struct {
	DurationSeconds       float64 `json:"duration_seconds"`
	SuccessRatePercent    float64 `json:"success_rate_percent"`
	SteadyCacheHitPercent float64 `json:"steady_cache_hit_percent"`
	TTFTP50MS             float64 `json:"ttft_p50_ms"`
	TTFTP90MS             float64 `json:"ttft_p90_ms"`
	TTFTP99MS             float64 `json:"ttft_p99_ms"`
	E2EP50MS              float64 `json:"e2e_p50_ms"`
	E2EP90MS              float64 `json:"e2e_p90_ms"`
	E2EP99MS              float64 `json:"e2e_p99_ms"`
	TPOTP50MS             float64 `json:"tpot_p50_ms"`
	TPOTP90MS             float64 `json:"tpot_p90_ms"`
	TPOTP99MS             float64 `json:"tpot_p99_ms"`
}

func validateDeferredLoadProfile(path string, raw []byte) error {
	var profile legacyLoadProfile
	if err := decodeStrictJSON(raw, &profile); err != nil {
		return fmt.Errorf("decode deferred load profile %s: %w", path, err)
	}
	if err := profile.validate(); err != nil {
		return fmt.Errorf("validate deferred load profile %s: %w", path, err)
	}
	return nil
}

func (profile legacyLoadProfile) validate() error {
	if strings.TrimSpace(profile.Name) == "" || strings.TrimSpace(profile.Name) != profile.Name ||
		profile.Model != "kimi-k3" || profile.Protocol != string(domain.ProtocolOpenAIChat) {
		return errors.New("name, model, and protocol are invalid")
	}
	request := domain.TestRequest{Method: domain.RequestPOST, Path: profile.RequestPath, Headers: map[string]string{}}
	if err := request.Validate(); err != nil {
		return fmt.Errorf("request path: %w", err)
	}
	if profile.InputTokensTarget <= 0 || profile.MaxOutputTokens <= 0 || profile.TurnsPerSession <= 0 ||
		profile.Sessions <= 0 || profile.TotalRequests <= 0 || profile.Concurrency <= 0 || profile.RampSeconds <= 0 {
		return errors.New("load dimensions must be positive")
	}
	if profile.TurnsPerSession > math.MaxInt/profile.Sessions || profile.TurnsPerSession*profile.Sessions != profile.TotalRequests {
		return errors.New("total requests must equal sessions times turns per session")
	}
	if profile.Concurrency > profile.TotalRequests {
		return errors.New("concurrency cannot exceed total requests")
	}
	if !positiveFinite(profile.ArrivalRatePerSecond.Start) || !positiveFinite(profile.ArrivalRatePerSecond.End) ||
		profile.ArrivalRatePerSecond.End < profile.ArrivalRatePerSecond.Start {
		return errors.New("arrival rate ramp is invalid")
	}
	reference := profile.ReferenceResult
	if !positiveFinite(reference.DurationSeconds) || !percentage(reference.SuccessRatePercent) ||
		!percentage(reference.SteadyCacheHitPercent) ||
		!orderedNonNegative(reference.TTFTP50MS, reference.TTFTP90MS, reference.TTFTP99MS) ||
		!orderedNonNegative(reference.E2EP50MS, reference.E2EP90MS, reference.E2EP99MS) ||
		!orderedNonNegative(reference.TPOTP50MS, reference.TPOTP90MS, reference.TPOTP99MS) {
		return errors.New("reference result is invalid")
	}
	return nil
}

func positiveFinite(value float64) bool {
	return value > 0 && !math.IsInf(value, 0) && !math.IsNaN(value)
}

func percentage(value float64) bool {
	return value >= 0 && value <= 100 && !math.IsInf(value, 0) && !math.IsNaN(value)
}

func orderedNonNegative(p50, p90, p99 float64) bool {
	return p50 >= 0 && p50 <= p90 && p90 <= p99 && !math.IsInf(p99, 0) && !math.IsNaN(p50) && !math.IsNaN(p90) && !math.IsNaN(p99)
}
