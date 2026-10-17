// Package remoteresults defines the durable import boundary for node journals.
package remoteresults

import (
	"context"
	"errors"
	"math"

	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

var (
	ErrInvalid  = errors.New("invalid remote result batch")
	ErrConflict = errors.New("remote result identity or content conflict")
	ErrNotFound = errors.New("remote result mirror not found")
)

// ConfigurationID identifies the local node record. NodeID and InstallationID
// identify the actual server and never change when an observer reconnects.
type Origin struct {
	ConfigurationID string `json:"configuration_id"`
	NodeID          string `json:"node_id"`
	InstallationID  string `json:"installation_id"`
}

func (origin Origin) Validate() error {
	validNode := domain.IsUUID(origin.ConfigurationID) && domain.IsUUID(origin.NodeID)
	if !validNode || !domain.IsUUID(origin.InstallationID) {
		return ErrInvalid
	}
	return nil
}

type Batch struct {
	Origin        Origin
	JobID         string
	AfterSequence uint64
	Events        []jobcontract.Event
}

func (batch Batch) Validate() error {
	validPage := batch.AfterSequence <= math.MaxInt64 && len(batch.Events) <= 200
	validIdentity := batch.Origin.Validate() == nil && domain.IsUUID(batch.JobID)
	if !validIdentity || !validPage {
		return ErrInvalid
	}
	sequence := batch.AfterSequence
	payloadBytes := 0
	for _, event := range batch.Events {
		payloadBytes += len(event.Payload)
		validEvent := event.JobID == batch.JobID && event.Sequence == sequence+1
		if !validEvent || payloadBytes > jobcontract.MaxEventPageBytes {
			return ErrInvalid
		}
		if event.Validate() != nil {
			return ErrInvalid
		}
		sequence = event.Sequence
	}
	return nil
}

type Mirror struct {
	Origin   Origin               `json:"origin"`
	Spec     jobcontract.JobSpec  `json:"spec"`
	Snapshot jobcontract.Snapshot `json:"snapshot"`
}

type Repository interface {
	ApplyRemoteBatch(context.Context, Batch) (Mirror, error)
	GetRemoteMirror(context.Context, string) (Mirror, error)
	ImportRemoteRunReport(context.Context, RunReportImport) error
	RemoteRunReportImported(context.Context, string, string) (bool, error)
	ImportRemotePerformanceDocument(context.Context, PerformanceDocumentImport) error
	ReadRemotePerformanceResult(context.Context, string) (jobcontract.PerformanceResult, error)
	ListRemoteDocuments(context.Context, string) ([]DocumentReference, error)
}

type RunReportImport struct {
	Origin   Origin
	JobID    string
	Document []byte
}

type PerformanceDocumentImport struct {
	Origin   Origin
	JobID    string
	Kind     jobcontract.EventKind
	Document []byte
}

// Kind distinguishes the complete PerformanceResult from its optional archive.
// Reference always identifies the original bytes; Imported does not imply that
// separate evidence or attachment files have been downloaded.
type DocumentReference struct {
	Kind      jobcontract.EventKind       `json:"kind"`
	Reference jobcontract.ReportReference `json:"reference"`
	Imported  bool                        `json:"imported"`
}
