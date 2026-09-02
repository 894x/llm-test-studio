package sqlite

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unsafe"

	"github.com/894x/llm-test-studio/internal/application/reporting"
	"github.com/894x/llm-test-studio/internal/domain"
)

func TestReportProjectionRetainedRunStateIsLightweight(t *testing.T) {
	typeOfExpectation := reflect.TypeOf(reportRunExpectation{})
	allowedTypes := map[reflect.Type]struct{}{
		reflect.TypeOf(""):                        {},
		reflect.TypeOf(uint64(0)):                 {},
		reflect.TypeOf([sha256SizeForTest]byte{}): {},
	}
	for index := 0; index < typeOfExpectation.NumField(); index++ {
		field := typeOfExpectation.Field(index)
		if _, allowed := allowedTypes[field.Type]; !allowed {
			t.Fatalf("reportRunExpectation retains non-lightweight field %s %s", field.Name, field.Type)
		}
	}
	if size := unsafe.Sizeof(reportRunExpectation{}); size > 128 {
		t.Fatalf("reportRunExpectation size = %d bytes, want <= 128", size)
	}
}

func TestReportProjectionQuerySortsBeforeLoadingWideDocuments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "projection-plan.db")
	if err := Migrate(context.Background(), path, MigrateOptions{AppVersion: "projection-plan-test"}); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	repository, err := OpenRepository(context.Background(), path, RepositoryOptions{})
	if err != nil {
		t.Fatalf("OpenRepository() error = %v", err)
	}
	defer repository.Close()

	rows, err := repository.conn.QueryContext(
		context.Background(),
		"EXPLAIN QUERY PLAN "+reportProjectionQuery,
		reporting.MaxSnapshotReports,
		domain.CurrentReportSchemaVersion,
		MaxReportProjectionDocumentBytes,
		domain.CurrentEntitySchemaVersion,
		domain.CurrentEntitySchemaVersion,
		MaxReportProjectionDocumentBytes,
	)
	if err != nil {
		t.Fatalf("EXPLAIN QUERY PLAN error = %v", err)
	}
	defer rows.Close()

	var details []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatalf("scan query plan: %v", err)
		}
		details = append(details, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate query plan: %v", err)
	}

	materializeIndex, reportLookupIndex := -1, -1
	for index, detail := range details {
		if strings.Contains(detail, "MATERIALIZE latest_reports") {
			materializeIndex = index
		}
		if strings.Contains(detail, "SEARCH report USING") {
			reportLookupIndex = index
		}
	}
	plan := strings.Join(details, "\n")
	if materializeIndex < 0 || reportLookupIndex < 0 || materializeIndex >= reportLookupIndex {
		t.Fatalf("latest IDs are not materialized before report lookup:\n%s", plan)
	}
	for index, detail := range details {
		if strings.Contains(detail, "USE TEMP B-TREE FOR ORDER BY") && index > reportLookupIndex {
			t.Fatalf("wide report rows enter an ORDER BY temporary sorter:\n%s", plan)
		}
	}
}

const sha256SizeForTest = 32
