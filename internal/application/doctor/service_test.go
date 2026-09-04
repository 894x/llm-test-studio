package doctor_test

import (
	"context"
	"errors"
	"testing"

	"github.com/894x/llm-test-studio/internal/application/doctor"
)

type fileSystemFunc func(context.Context, string) (bool, error)

func (filesystem fileSystemFunc) DirectoryHasEntries(ctx context.Context, path string) (bool, error) {
	return filesystem(ctx, path)
}

type catalogFunc func(context.Context, string, string) (int, error)

func (catalog catalogFunc) CountCases(ctx context.Context, root, suite string) (int, error) {
	return catalog(ctx, root, suite)
}

func TestInspectReturnsTypedReadyResultWhenOneSupportedSuiteLoads(t *testing.T) {
	t.Parallel()

	var inspectedRoot string
	service := doctor.New(doctor.Dependencies{
		FileSystem: fileSystemFunc(func(_ context.Context, path string) (bool, error) {
			inspectedRoot = path
			return true, nil
		}),
		Catalog: catalogFunc(func(_ context.Context, root, suite string) (int, error) {
			if root != "case-root" {
				t.Fatalf("catalog root = %q", root)
			}
			if suite == "openai-chat" {
				return 2, nil
			}
			return 0, errors.New("suite unavailable")
		}),
	})

	result := service.Inspect(context.Background(), doctor.InspectRequest{CasesRoot: "case-root"})
	if inspectedRoot != "case-root" {
		t.Fatalf("filesystem root = %q", inspectedRoot)
	}
	if result.SchemaVersion != doctor.SchemaVersion || result.Status != doctor.OverallReady {
		t.Fatalf("result metadata = version %d status %q", result.SchemaVersion, result.Status)
	}
	want := map[doctor.CheckName]doctor.CheckStatus{
		doctor.CheckBinary:          doctor.CheckPass,
		doctor.CheckGoCore:          doctor.CheckPass,
		doctor.CheckCases:           doctor.CheckPass,
		doctor.CheckDatabase:        doctor.CheckNotConfigured,
		doctor.CheckCredentialStore: doctor.CheckNotConfigured,
	}
	if len(result.Checks) != len(want) {
		t.Fatalf("check count = %d, want %d", len(result.Checks), len(want))
	}
	for _, check := range result.Checks {
		if check.Status != want[check.Name] {
			t.Errorf("check %q status = %q, want %q", check.Name, check.Status, want[check.Name])
		}
	}
}

func TestInspectRecognizesWanVideoAsABuiltinSuite(t *testing.T) {
	t.Parallel()

	service := doctor.New(doctor.Dependencies{
		FileSystem: fileSystemFunc(func(context.Context, string) (bool, error) { return true, nil }),
		Catalog: catalogFunc(func(_ context.Context, _, suite string) (int, error) {
			if suite == "wan-video" {
				return 30, nil
			}
			return 0, nil
		}),
	})

	result := service.Inspect(context.Background(), doctor.InspectRequest{CasesRoot: "cases"})
	if result.Status != doctor.OverallReady {
		t.Fatalf("status = %q, want ready", result.Status)
	}
}

func TestInspectFailsSafelyForMissingPortsEmptyRootsAndCancellation(t *testing.T) {
	t.Parallel()

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	tests := []struct {
		name         string
		ctx          context.Context
		dependencies doctor.Dependencies
		wantBinary   doctor.CheckStatus
		wantCore     doctor.CheckStatus
		wantCases    doctor.CheckStatus
	}{
		{
			name:       "missing ports",
			ctx:        nil,
			wantBinary: doctor.CheckPass,
			wantCore:   doctor.CheckFail,
			wantCases:  doctor.CheckFail,
		},
		{
			name: "empty root",
			ctx:  context.Background(),
			dependencies: doctor.Dependencies{
				FileSystem: fileSystemFunc(func(context.Context, string) (bool, error) { return false, nil }),
				Catalog: catalogFunc(func(context.Context, string, string) (int, error) {
					t.Fatal("catalog must not run for an empty root")
					return 0, nil
				}),
			},
			wantBinary: doctor.CheckPass,
			wantCore:   doctor.CheckPass,
			wantCases:  doctor.CheckFail,
		},
		{
			name: "caller canceled",
			ctx:  canceled,
			dependencies: doctor.Dependencies{
				FileSystem: fileSystemFunc(func(context.Context, string) (bool, error) {
					t.Fatal("filesystem must not run after cancellation")
					return false, nil
				}),
				Catalog: catalogFunc(func(context.Context, string, string) (int, error) { return 1, nil }),
			},
			wantBinary: doctor.CheckFail,
			wantCore:   doctor.CheckPass,
			wantCases:  doctor.CheckFail,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			result := doctor.New(test.dependencies).Inspect(test.ctx, doctor.InspectRequest{CasesRoot: "cases"})
			if result.Status != doctor.OverallFailed {
				t.Fatalf("status = %q, want failed", result.Status)
			}
			assertDoctorCheck(t, result, doctor.CheckBinary, test.wantBinary)
			assertDoctorCheck(t, result, doctor.CheckGoCore, test.wantCore)
			assertDoctorCheck(t, result, doctor.CheckCases, test.wantCases)
			assertDoctorCheck(t, result, doctor.CheckDatabase, doctor.CheckNotConfigured)
			assertDoctorCheck(t, result, doctor.CheckCredentialStore, doctor.CheckNotConfigured)
		})
	}
}

func TestInspectRejectsSuccessfulPortValuesWhenCallerCancelsBeforeReturn(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name         string
		dependencies func(*testing.T, context.CancelFunc) doctor.Dependencies
	}{
		{
			name: "filesystem cancels before returning entries",
			dependencies: func(t *testing.T, cancel context.CancelFunc) doctor.Dependencies {
				return doctor.Dependencies{
					FileSystem: fileSystemFunc(func(context.Context, string) (bool, error) {
						cancel()
						return true, nil
					}),
					Catalog: catalogFunc(func(context.Context, string, string) (int, error) {
						t.Fatal("catalog must not run after filesystem cancellation")
						return 0, nil
					}),
				}
			},
		},
		{
			name: "catalog cancels before returning a positive count",
			dependencies: func(_ *testing.T, cancel context.CancelFunc) doctor.Dependencies {
				return doctor.Dependencies{
					FileSystem: fileSystemFunc(func(context.Context, string) (bool, error) { return true, nil }),
					Catalog: catalogFunc(func(context.Context, string, string) (int, error) {
						cancel()
						return 1, nil
					}),
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(context.Background())
			result := doctor.New(test.dependencies(t, cancel)).Inspect(ctx, doctor.InspectRequest{CasesRoot: "cases"})
			if result.Status != doctor.OverallFailed {
				t.Fatalf("status = %q, want failed", result.Status)
			}
			assertDoctorCheck(t, result, doctor.CheckBinary, doctor.CheckFail)
			assertDoctorCheck(t, result, doctor.CheckGoCore, doctor.CheckPass)
			assertDoctorCheck(t, result, doctor.CheckCases, doctor.CheckFail)
		})
	}
}

func assertDoctorCheck(t *testing.T, result doctor.Result, name doctor.CheckName, want doctor.CheckStatus) {
	t.Helper()
	for _, check := range result.Checks {
		if check.Name == name {
			if check.Status != want {
				t.Fatalf("check %q status = %q, want %q", name, check.Status, want)
			}
			return
		}
	}
	t.Fatalf("check %q is missing", name)
}
