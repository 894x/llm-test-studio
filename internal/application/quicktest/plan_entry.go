package quicktest

import (
	"encoding/json"
	"errors"

	"github.com/894x/llm-test-studio/internal/domain"
)

// PreparePlanEntry resolves a frozen plan item's profile without touching the
// catalog or credentials. The enclosing Run owns the execution and its report.
func PreparePlanEntry(id string, channel domain.ChannelSnapshot, profile domain.PerformancePlanProfile) (PreparedPerformance, error) {
	address, code := normalizeAddress(channel.Protocol, AddressModeBaseURL, channel.BaseURL)
	if code != "" {
		return PreparedPerformance{}, errors.New("invalid performance entry address")
	}
	raw, err := json.Marshal(profile)
	if err != nil {
		return PreparedPerformance{}, err
	}
	var executionProfile PerformanceProfile
	if err := json.Unmarshal(raw, &executionProfile); err != nil {
		return PreparedPerformance{}, err
	}
	prepared := PreparedPerformance{
		ReportID: id, Protocol: channel.Protocol, AddressMode: AddressModeBaseURL,
		BaseURL: address.baseURL, Endpoint: address.endpoint,
		ModelID: channel.UpstreamModelName, Profile: executionProfile,
	}
	return prepared, prepared.Validate()
}

// ValidatePlanProfile checks authored performance options before saving a plan.
func ValidatePlanProfile(profile domain.PerformancePlanProfile) error {
	raw, err := json.Marshal(profile)
	if err != nil {
		return err
	}
	var execution PerformanceProfile
	if err := json.Unmarshal(raw, &execution); err != nil {
		return err
	}
	_, _, err = preparePerformanceProfile(execution)
	return err
}
