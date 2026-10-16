package domain

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func originForTest() ExecutionOrigin {
	return ExecutionOrigin{Kind: "remote", NodeID: "11111111-1111-4111-8111-111111111111",
		InstallationID: "22222222-2222-4222-8222-222222222222", EngineBuildID: strings.Repeat("a", 64),
		RunnerVersion: "runner-origin-test", OS: "linux", Arch: "amd64"}
}

func TestExecutionOriginRequiresExplicitIdentityAndPlatform(t *testing.T) {
	for _, change := range []func(*ExecutionOrigin){
		func(origin *ExecutionOrigin) { origin.Kind = "" },
		func(origin *ExecutionOrigin) { origin.Kind = "unknown" },
		func(origin *ExecutionOrigin) { origin.NodeID = "" },
		func(origin *ExecutionOrigin) { origin.InstallationID = origin.InstallationID[:35] },
		func(origin *ExecutionOrigin) { origin.EngineBuildID = strings.Repeat("A", 64) },
		func(origin *ExecutionOrigin) { origin.RunnerVersion = "" },
		func(origin *ExecutionOrigin) { origin.RunnerVersion = "runner\nsecret" },
		func(origin *ExecutionOrigin) { origin.RunnerVersion = strings.Repeat("v", 129) },
		func(origin *ExecutionOrigin) { origin.OS = "" },
		func(origin *ExecutionOrigin) { origin.OS = string([]byte{0xff}) },
		func(origin *ExecutionOrigin) { origin.Arch = " amd64" },
	} {
		origin := originForTest()
		change(&origin)
		if origin.Validate() == nil {
			t.Fatalf("invalid execution origin accepted: %#v", origin)
		}
	}
	for _, kind := range []string{"local", "remote"} {
		origin := originForTest()
		origin.Kind = kind
		if err := origin.Validate(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReportExecutionOriginIsExplicitlyUnmanagedOrValid(t *testing.T) {
	report := validReport()
	document, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	fields := map[string]json.RawMessage{}
	if json.Unmarshal(document, &fields) != nil || !bytes.Equal(fields["execution_origin"], []byte("null")) {
		t.Fatal("unmanaged current report omitted its explicit origin field")
	}
	origin := originForTest()
	report.ExecutionOrigin = &origin
	if err := report.Validate(); err != nil {
		t.Fatal(err)
	}
	origin.NodeID = ""
	if report.Validate() == nil {
		t.Fatal("report accepted an incomplete managed execution origin")
	}
}
