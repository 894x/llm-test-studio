package bootstrap

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/reporting"
)

type documentGateProbe struct{}

func (documentGateProbe) Snapshot(context.Context) (reporting.Snapshot, error) {
	return reporting.Snapshot{}, nil
}

func (documentGateProbe) Detail(context.Context, string) (reporting.Detail, error) {
	return reporting.Detail{}, nil
}

func (documentGateProbe) Export(context.Context, string, reporting.ExportFormat, string) (reporting.ExportedDocument, error) {
	return reporting.ExportedDocument{}, nil
}

func (documentGateProbe) ExportLocalized(context.Context, string, reporting.ExportFormat, string, string) (reporting.ExportedDocument, error) {
	return reporting.ExportedDocument{}, nil
}

func TestReportDocumentsDoNotWaitForTheAuthoredCatalogGate(t *testing.T) {
	for _, operation := range []string{"detail", "export", "localized_export"} {
		t.Run(operation, func(t *testing.T) {
			gate := &productionServiceGate{}
			release := sync.OnceFunc(gate.enter())
			defer release()
			query := serializedReportingQuery{gate: gate, query: documentGateProbe{}}
			done := make(chan error, 1)
			go func() {
				var err error
				switch operation {
				case "detail":
					_, err = query.Detail(t.Context(), "report")
				case "export":
					_, err = query.Export(t.Context(), "report", reporting.ExportJSON, "")
				default:
					_, err = query.ExportLocalized(t.Context(), "report", reporting.ExportJSON, "", "zh-CN")
				}
				done <- err
			}()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				release()
				<-done
				t.Fatal("report document waited for an unrelated catalog workflow")
			}
		})
	}
}
