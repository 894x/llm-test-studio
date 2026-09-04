package catalog

import (
	"context"
	"errors"
	"reflect"
	"time"

	"github.com/894x/llm-test-studio/internal/casetypes"
	"github.com/894x/llm-test-studio/internal/domain"
)

var (
	ErrInvalid     = errors.New("catalog: invalid input")
	ErrNotFound    = errors.New("catalog: record not found")
	ErrConflict    = errors.New("catalog: revision conflict")
	ErrCorrupt     = errors.New("catalog: inconsistent data")
	ErrUnavailable = errors.New("catalog: repository unavailable")
)

// Repository is implemented structurally by persistence adapters, including
// sqlite.Repository. Its values remain inside the application layer.
type Repository interface {
	ListModels(context.Context) ([]domain.Model, error)
	ListChannels(context.Context) ([]domain.Channel, error)
	ListChannelModels(context.Context) ([]domain.ChannelModel, error)
	ListTestCases(context.Context) ([]domain.TestCase, error)
	ListSuites(context.Context) ([]domain.Suite, error)
	ListPlans(context.Context) ([]domain.Plan, error)

	GetModel(context.Context, string) (domain.Model, error)
	GetChannel(context.Context, string) (domain.Channel, error)
	GetChannelModel(context.Context, string) (domain.ChannelModel, error)
	GetTestCase(context.Context, string) (domain.TestCase, error)
	GetSuite(context.Context, string) (domain.Suite, error)
	GetPlan(context.Context, string) (domain.Plan, error)

	CreateModel(context.Context, domain.Model) error
	CreateChannel(context.Context, domain.Channel) error
	CreateChannelModel(context.Context, domain.ChannelModel) error
	CreateTestCase(context.Context, domain.TestCase) error
	CreateSuite(context.Context, domain.Suite) error
	CreatePlan(context.Context, domain.Plan) error

	UpdateModel(context.Context, uint64, domain.Model) error
	UpdateChannel(context.Context, uint64, domain.Channel) error
	UpdateChannelModel(context.Context, uint64, domain.ChannelModel) error
	UpdateTestCase(context.Context, uint64, domain.TestCase) error
	UpdateSuite(context.Context, uint64, domain.Suite) error
	UpdatePlan(context.Context, uint64, domain.Plan) error

	DeleteModel(context.Context, string, uint64) error
	DeleteChannel(context.Context, string, uint64) error
	DeleteChannelModel(context.Context, string, uint64) error
	DeleteTestCase(context.Context, string, uint64) error
	DeleteSuite(context.Context, string, uint64) error
	DeletePlan(context.Context, string, uint64) error
}

type Clock interface {
	Now() time.Time
}

type MetaFactory func(time.Time) (domain.EntityMeta, error)

// RepositoryErrorSet lets an adapter declare its private error sentinels
// without making the application layer depend on that adapter.
type RepositoryErrorSet struct {
	NotFound error
	Conflict error
	Corrupt  error
}

type Dependencies struct {
	Repository       Repository
	Clock            Clock
	MetaFactory      MetaFactory
	RepositoryErrors RepositoryErrorSet
	CaseTypes        *casetypes.Registry
}

type Service struct {
	repository       Repository
	clock            Clock
	metaFactory      MetaFactory
	repositoryErrors RepositoryErrorSet
	caseTypes        *casetypes.Registry
}

func New(dependencies Dependencies) (*Service, error) {
	if isNilInterface(dependencies.Repository) || isNilInterface(dependencies.Clock) {
		return nil, ErrInvalid
	}
	factory := dependencies.MetaFactory
	if factory == nil {
		factory = domain.NewEntityMeta
	}
	caseTypes := dependencies.CaseTypes
	if caseTypes == nil {
		caseTypes = casetypes.MustBuiltinRegistry()
	}
	return &Service{
		repository: dependencies.Repository, clock: dependencies.Clock,
		metaFactory: factory, repositoryErrors: dependencies.RepositoryErrors, caseTypes: caseTypes,
	}, nil
}

func isNilInterface(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
