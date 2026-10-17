package jobcontent

import (
	"context"
	"io"

	"github.com/894x/llm-test-studio/internal/jobcontract"
)

// PhysicalStore owns one private root. The runtime closes it after Manager.Close.
type PhysicalStore interface {
	Store() Store
	Ready(context.Context) error
	PutContent(context.Context, jobcontract.ContentIdentity, io.Reader) (bool, error)
	OpenPinned(context.Context, jobcontract.ContentIdentity) (PinnedContent, error)
}

// PinnedContent retains a fully hashed file descriptor. Each open has its own
// offset; Object is immutable evidence, and Check verifies the live private file.
type PinnedContent interface {
	io.ReadSeekCloser
	Object() ObjectIdentity
	Check() error
}
