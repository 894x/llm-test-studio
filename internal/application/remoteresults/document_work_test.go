package remoteresults

import (
	"testing"

	"github.com/894x/llm-test-studio/internal/jobcontract"
)

func TestDocumentCursorAcceptsOnlyZeroOrCompleteCurrentIdentity(t *testing.T) {
	valid := DocumentCursor{JobID: "11111111-1111-4111-8111-111111111111",
		ReferenceType: jobcontract.EventReport, ReportID: "22222222-2222-4222-8222-222222222222"}
	for _, cursor := range []DocumentCursor{{}, valid} {
		if err := cursor.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	for _, cursor := range []DocumentCursor{
		{JobID: valid.JobID}, {ReportID: valid.ReportID},
		{JobID: valid.JobID, ReportID: valid.ReportID},
		{JobID: valid.JobID, ReferenceType: jobcontract.EventAccepted, ReportID: valid.ReportID},
		{JobID: "not-an-id", ReferenceType: valid.ReferenceType, ReportID: valid.ReportID},
	} {
		if cursor.Validate() == nil {
			t.Fatal("partial or unsupported document cursor accepted")
		}
	}
	origin := Origin{ConfigurationID: valid.JobID, NodeID: valid.ReportID, InstallationID: valid.JobID}
	for _, limit := range []uint32{1, 25} {
		if err := (DocumentQuery{Origin: origin, Class: DocumentFocus, Limit: limit}).Validate(); err != nil {
			t.Fatal(err)
		}
	}
	for _, query := range []DocumentQuery{
		{Origin: origin, Class: DocumentHistory},
		{Origin: origin, Class: DocumentFocus, Limit: 26},
		{Origin: origin, Class: "unknown", Limit: 2},
		{Class: DocumentFocus, Limit: 2},
	} {
		if query.Validate() == nil {
			t.Fatal("invalid document query accepted")
		}
	}
}
