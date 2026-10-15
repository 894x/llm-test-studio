package jobcontract

import (
	"bytes"
	"encoding/json"

	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/domain"
)

const MaxRunReportBytes = 32 << 20

const FeaturePerformanceArchives = "performance_archives"

func EncodePerformanceArchive(report quicktest.PerformanceReport) ([]byte, error) {
	if _, err := quicktest.ValidateArchivedPerformanceReport(report); err != nil {
		return nil, invalid("performance_archive")
	}
	raw, err := json.Marshal(report)
	if err != nil || len(raw) > MaxRunReportBytes {
		return nil, invalid("performance_archive.bytes")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, invalid("performance_archive.bytes")
	}
	return json.Marshal(value)
}

// EncodeRunReport verifies the current report and returns its canonical bytes.
// Downloads retain stored bytes; this encoder only verifies their integrity.
func EncodeRunReport(report domain.Report) ([]byte, error) {
	if err := report.Validate(); err != nil {
		return nil, invalid("report")
	}
	raw, err := json.Marshal(report)
	if err != nil || len(raw) > MaxRunReportBytes {
		return nil, invalid("report.bytes")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, invalid("report.bytes")
	}
	return json.Marshal(value)
}
