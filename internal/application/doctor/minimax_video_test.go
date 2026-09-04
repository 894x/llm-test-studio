package doctor_test

import (
	"context"
	"testing"

	"github.com/894x/llm-test-studio/internal/application/doctor"
)

func TestInspectRecognizesMiniMaxVideoAsABuiltinSuite(t *testing.T) {
	service := doctor.New(doctor.Dependencies{
		FileSystem: fileSystemFunc(func(context.Context, string) (bool, error) { return true, nil }),
		Catalog: catalogFunc(func(_ context.Context, _, suite string) (int, error) {
			if suite == "minimax-video" {
				return 149, nil
			}
			return 0, nil
		}),
	})

	result := service.Inspect(context.Background(), doctor.InspectRequest{CasesRoot: "cases"})
	if result.Status != doctor.OverallReady {
		t.Fatalf("status = %q, want ready", result.Status)
	}
}
