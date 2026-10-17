package jobcontent

import (
	"context"
	"io"

	"github.com/894x/llm-test-studio/internal/jobcontract"
)

// Ledger contains metadata operations. Implementations never open or hash files.
type Ledger interface {
	RegisterContentStore(context.Context, Store) error
	// The bool identifies a new publication generation, not a new physical file.
	PublishContent(context.Context, Publication) (Generation, bool, error)
	ContentPublication(context.Context, string, string) (Generation, error)
	JobContent(context.Context, string) (Inventory, error)
}

type AssetResolver interface {
	Open(context.Context, string) (AssetStream, error)
}

type AssetStream interface {
	io.ReadCloser
	Identity() jobcontract.ContentIdentity
	MediaType() string
}

// AdmissionResources is called outside the Host mutex and database transaction.
type AdmissionResources interface {
	Prepare(context.Context, jobcontract.JobSpec) (PreparedResources, error)
}

// PreparedResources owns physical pins until BindJob succeeds or Close returns.
// Its metadata does not authorize admission without the owning manager permit.
type PreparedResources interface {
	Claims() []Claim
	Permit() *Permit
	BindJob(string) (JobResources, error)
	Close() error
}

type JobResources interface {
	Resolver() AssetResolver
	Close() error
}
