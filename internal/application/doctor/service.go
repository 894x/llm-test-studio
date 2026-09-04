package doctor

import "context"

const SchemaVersion = 1

type CheckName string

const (
	CheckBinary          CheckName = "binary"
	CheckGoCore          CheckName = "go_core"
	CheckCases           CheckName = "cases"
	CheckDatabase        CheckName = "database"
	CheckCredentialStore CheckName = "credential_store"
)

type CheckStatus string

const (
	CheckPass          CheckStatus = "pass"
	CheckFail          CheckStatus = "fail"
	CheckNotConfigured CheckStatus = "not_configured"
)

type OverallStatus string

const (
	OverallReady  OverallStatus = "ready"
	OverallFailed OverallStatus = "failed"
)

type Check struct {
	Name   CheckName   `json:"name"`
	Label  string      `json:"label"`
	Status CheckStatus `json:"status"`
}

type Result struct {
	SchemaVersion int           `json:"schema_version"`
	Status        OverallStatus `json:"status"`
	Checks        []Check       `json:"checks"`
}

type InspectRequest struct {
	CasesRoot string
}

type FileSystem interface {
	DirectoryHasEntries(context.Context, string) (bool, error)
}

type Catalog interface {
	CountCases(context.Context, string, string) (int, error)
}

type Dependencies struct {
	FileSystem FileSystem
	Catalog    Catalog
	Suites     []string
}

type Service struct {
	filesystem FileSystem
	catalog    Catalog
	suites     []string
}

func New(dependencies Dependencies) *Service {
	suites := append([]string(nil), dependencies.Suites...)
	if len(suites) == 0 {
		suites = []string{"openai-chat", "kimi-k3", "seedance", "wan-video", "minimax-video"}
	}
	return &Service{filesystem: dependencies.FileSystem, catalog: dependencies.Catalog, suites: suites}
}

func (service *Service) Inspect(ctx context.Context, request InspectRequest) Result {
	ctx = nonNilContext(ctx)
	result := newResult()
	if service != nil && service.catalog != nil {
		setCheck(&result, CheckGoCore, CheckPass)
	}
	if err := ctx.Err(); err != nil {
		setCheck(&result, CheckBinary, CheckFail)
		return finalizeInspection(ctx, result)
	}
	if service == nil || service.filesystem == nil || service.catalog == nil {
		return finalizeInspection(ctx, result)
	}
	hasEntries, err := service.filesystem.DirectoryHasEntries(ctx, request.CasesRoot)
	if ctx.Err() != nil {
		return finalizeInspection(ctx, result)
	}
	if err != nil || !hasEntries {
		return finalizeInspection(ctx, result)
	}
	for _, suite := range service.suites {
		count, err := service.catalog.CountCases(ctx, request.CasesRoot, suite)
		if ctx.Err() != nil {
			return finalizeInspection(ctx, result)
		}
		if err == nil && count > 0 {
			setCheck(&result, CheckCases, CheckPass)
			break
		}
	}
	return finalizeInspection(ctx, result)
}

func newResult() Result {
	return Result{
		SchemaVersion: SchemaVersion,
		Status:        OverallFailed,
		Checks: []Check{
			{Name: CheckBinary, Label: "Binary", Status: CheckPass},
			{Name: CheckGoCore, Label: "Go core", Status: CheckFail},
			{Name: CheckCases, Label: "Cases", Status: CheckFail},
			{Name: CheckDatabase, Label: "Database", Status: CheckNotConfigured},
			{Name: CheckCredentialStore, Label: "Credential store", Status: CheckNotConfigured},
		},
	}
}

func setCheck(result *Result, name CheckName, status CheckStatus) {
	for index := range result.Checks {
		if result.Checks[index].Name == name {
			result.Checks[index].Status = status
			return
		}
	}
}

func finalizeInspection(ctx context.Context, result Result) Result {
	if ctx.Err() != nil {
		setCheck(&result, CheckBinary, CheckFail)
		setCheck(&result, CheckCases, CheckFail)
	}
	return finalize(result)
}

func finalize(result Result) Result {
	passed := 0
	for _, check := range result.Checks {
		if (check.Name == CheckBinary || check.Name == CheckGoCore || check.Name == CheckCases) && check.Status == CheckPass {
			passed++
		}
	}
	if passed == 3 {
		result.Status = OverallReady
	}
	return result
}

func nonNilContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}
