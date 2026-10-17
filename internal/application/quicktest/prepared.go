package quicktest

import (
	"errors"
	"strings"

	"github.com/894x/llm-test-studio/internal/domain"
)

var ErrInvalidPreparedPerformance = errors.New("quick test: invalid prepared performance input")

// PreparedPerformance contains resolved, non-secret execution input. It has no
// references that require a desktop catalog, remembered key, or Suite resolver.
type PreparedPerformance struct {
	ReportID    string             `json:"report_id"`
	Protocol    domain.Protocol    `json:"protocol"`
	AddressMode AddressMode        `json:"address_mode"`
	BaseURL     string             `json:"base_url"`
	Endpoint    string             `json:"endpoint"`
	ModelID     string             `json:"model_id"`
	Profile     PerformanceProfile `json:"profile"`
}

func preparedPerformance(report PerformanceReport, id string) PreparedPerformance {
	return PreparedPerformance{
		ReportID: id, Protocol: report.Protocol, AddressMode: report.AddressMode,
		BaseURL: report.BaseURL, Endpoint: report.Endpoint, ModelID: report.ModelID, Profile: report.Profile,
	}
}

func (prepared PreparedPerformance) Validate() error {
	validIdentity := domain.IsUUID(prepared.ReportID) && SupportsPerformance(prepared.Protocol)
	validModel := prepared.ModelID != "" && prepared.ModelID == strings.TrimSpace(prepared.ModelID)
	validMode := prepared.AddressMode == AddressModeBaseURL || prepared.AddressMode == AddressModeFullURL
	if !validIdentity || !validModel || !validMode {
		return errors.New("invalid prepared performance identity")
	}
	address, code := normalizeAddress(prepared.Protocol, AddressModeFullURL, prepared.Endpoint)
	if code != "" || address.baseURL != prepared.BaseURL || address.endpoint != prepared.Endpoint {
		return errors.New("invalid prepared performance address")
	}
	if _, _, err := preparePerformanceProfile(prepared.Profile); err != nil {
		return err
	}
	return nil
}

func (prepared PreparedPerformance) report() PerformanceReport {
	return PerformanceReport{
		ReportID:      prepared.ReportID,
		SchemaVersion: PerformanceSchemaVersion, Protocol: prepared.Protocol, AddressMode: prepared.AddressMode,
		BaseURL: prepared.BaseURL, Endpoint: prepared.Endpoint, ModelID: prepared.ModelID, Profile: prepared.Profile,
		ArchiveStatus: PerformanceArchiveNotAttempted, Failures: []PerformanceFailure{}, Samples: []PerformanceSample{},
		Progress: PerformanceProgress{Phase: PerformancePhaseNotStarted},
	}
}
