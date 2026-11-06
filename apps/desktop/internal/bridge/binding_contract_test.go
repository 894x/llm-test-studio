package bridge

import (
	"reflect"
	"slices"
	"testing"
)

// The desktop client depends on this method set. Host hooks must stay package-level.
func TestDesktopBindingMethodSetSurvivesPackageMove(t *testing.T) {
	want := []string{
		"BackupAndCreateDatabase",
		"CancelBenchmark",
		"ConfirmRemoteNodeHostKey",
		"ControlJob",
		"CopyReportPNG",
		"CreateChannel",
		"CreateChannelModel",
		"CreateLocalFileSelection",
		"CreateModel",
		"CreatePlan",
		"CreateSuite",
		"CreateTestCase",
		"DeleteChannel",
		"DeleteChannelModel",
		"DeleteModel",
		"DeletePlan",
		"DeleteSuite",
		"DeleteTestCase",
		"ExportBenchmarkReport", "ExportReport",
		"ForgetQuickTaskCredential",
		"GetAvailableReports",
		"GetBenchmark", "GetBenchmarkCatalog", "GetBenchmarkReport", "GetBenchmarks",
		"GetCatalog",
		"GetComparisonHistory",
		"GetComparisons",
		"GetDiagnostics",
		"GetJob",
		"GetJobControls",
		"GetJobPerformanceResult",
		"GetJobs",
		"GetOverview",
		"GetQuickTask", "GetQuickTestHistory",
		"GetRecycledHistory",
		"GetRemoteNodeUpgrade",
		"GetRemoteNodes",
		"GetReportDetail",
		"GetReportGeneration",
		"GetReports",
		"GetRunHistory",
		"GetRunSummaries",
		"GetStartupProgress",
		"GetWorkspace",
		"LaunchJob",
		"OpenDatabaseRecoveryDirectory",
		"OpenDiagnosticsDirectory",
		"PrepareBenchmarkRuntime",
		"PreviewHistoryRecycle",
		"RecoverRemoteNodeUpgrade",
		"RecycleHistory",
		"RefreshRemoteNode",
		"ReleaseLocalFileSelection",
		"RememberQuickTaskCredential",
		"RemoveRemoteNode",
		"ReportFrontendDiagnostic",
		"RestoreHistory",
		"RetryJobControl",
		"RetryRemoteInstallation",
		"RetryRemoteNodeUpgrade",
		"SaveQuickTestTarget",
		"SaveRemoteNode",
		"SaveReportExport",
		"SelectDefaultExecution",
		"SetRemoteNodeTaskConcurrency",
		"StartBenchmark",
		"StartRemoteNodeUpgrade",
		"UpdateChannel",
		"UpdateChannelModel",
		"UpdateModel",
		"UpdatePlan",
		"UpdateSuite",
		"UpdateTestCase",
	}
	bound := reflect.TypeOf((*DesktopApp)(nil))
	got := make([]string, bound.NumMethod())
	for i := range got {
		got[i] = bound.Method(i).Name
	}
	if !slices.Equal(got, want) {
		t.Fatalf("desktop binding methods = %v, want %v", got, want)
	}
}
