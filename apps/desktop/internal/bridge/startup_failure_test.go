package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestStartupFailureExposesOnlyBoundedCategoryAndRetainsDiagnostics(t *testing.T) {
	cause := errors.New("private/path sk-secret provider body")
	for _, test := range []struct {
		code, stage, want string
		from, to          int
	}{
		{code: "workspace_busy", stage: "opening", want: "workspace_busy"},
		{code: "disk_full", stage: "database_backing_up", want: "disk_full"},
		{code: "unknown", stage: "database_backing_up", want: "backup_failed"},
		{code: "unknown", stage: "database_migrating", want: "migration_failed"},
		{code: "unknown", stage: "database_validating", from: 1, to: 3, want: "migration_failed"},
		{code: "unknown", stage: "database_validating", from: 0, to: 3, want: "unknown"},
		{code: "unknown", stage: "database_validating", from: 3, to: 3, want: "unknown"},
		{code: "sk-private-secret", stage: "opening", want: "unknown"},
	} {
		t.Run(test.code+"/"+test.stage, func(t *testing.T) {
			app := New(func(ctx context.Context) (Dependencies, error) {
				ReportStartupProgress(ctx, test.stage, test.from, test.to)
				return Dependencies{}, &StartupFailure{Code: test.code, Cause: cause}
			})
			var reported error
			SetErrorReporter(app, func(err error) { reported = err })
			app.onStartup(t.Context())
			defer app.shutdown()
			progress := app.GetStartupProgress()
			if progress.FailureCode != test.want || !errors.Is(reported, cause) {
				t.Fatalf("failure = %+v, reported cause lost = %v", progress, reported)
			}
			encoded, err := json.Marshal(progress)
			if err != nil {
				t.Fatal(err)
			}
			for _, secret := range []string{"private/path", "sk-", "provider body"} {
				if strings.Contains(string(encoded), secret) {
					t.Fatal("startup DTO exposed a private diagnostic")
				}
			}
		})
	}
}
