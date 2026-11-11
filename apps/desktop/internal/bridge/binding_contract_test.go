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
		"CancelDesktopUpdate",
		"CheckDesktopUpdate",
		"CompareReports",
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
		"DownloadDesktopUpdate",
		"ExportBenchmarkReport", "ExportReport",
		"ForgetQuickTaskCredential",
		"GetAvailableReports",
		"GetBenchmark", "GetBenchmarkCatalog", "GetBenchmarkDatasets", "GetBenchmarkReport", "GetBenchmarkReportOwner", "GetBenchmarks",
		"GetCatalog",
		"GetComparisonHistory",
		"GetComparisons",
		"GetDesktopUpdate",
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
		"InstallDesktopUpdate",
		"LaunchJob",
		"ManageBenchmarkDataset",
		"OpenDatabaseRecoveryDirectory",
		"OpenDesktopUpdateRelease",
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
		"SetDesktopUpdatePreferences",
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
