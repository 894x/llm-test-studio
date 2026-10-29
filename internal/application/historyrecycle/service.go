// Package historyrecycle owns client-local, recoverable history visibility.
package historyrecycle

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
)

const PageSize = 25

var (
	ErrInvalid     = errors.New("invalid history recycle target")
	ErrNotFound    = errors.New("history recycle target not found")
	ErrActive      = errors.New("history recycle target is active")
	ErrUnconfirmed = errors.New("history recycle execution is unconfirmed")
)

type Target struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

func (target Target) Validate() error {
	if !domain.IsUUID(target.ID) {
		return ErrInvalid
	}
	switch target.Kind {
	case "job", "run", "report", "comparison":
		return nil
	}
	return ErrInvalid
}

type Entry struct {
	Kind          string   `json:"kind"`
	ID            string   `json:"id"`
	Title         string   `json:"title"`
	RemovedAt     string   `json:"removed_at"`
	RunIDs        []string `json:"run_ids"`
	ReportIDs     []string `json:"report_ids"`
	ComparisonIDs []string `json:"comparison_ids"`
	JobIDs        []string `json:"job_ids"`
	Remote        bool     `json:"remote"`
}

type Preview struct {
	SchemaVersion int    `json:"schema_version"`
	Entry         Entry  `json:"entry"`
	CanRecycle    bool   `json:"can_recycle"`
	BlockedReason string `json:"blocked_reason"`
}

type Page struct {
	SchemaVersion int     `json:"schema_version"`
	Entries       []Entry `json:"entries"`
	NextCursor    string  `json:"next_cursor"`
	HasMore       bool    `json:"has_more"`
}

type Repository interface {
	PreviewHistoryRecycle(context.Context, Target) (Preview, error)
	RecycleHistory(context.Context, Target, time.Time) (Entry, error)
	RestoreHistory(context.Context, Target) (Entry, error)
	ListRecycledHistory(context.Context, string) (Page, error)
}

type Service struct{ repository Repository }

func New(repository Repository) *Service { return &Service{repository: repository} }

func (service *Service) Preview(ctx context.Context, target Target) (Preview, error) {
	if err := target.Validate(); err != nil {
		return Preview{}, err
	}
	return service.repository.PreviewHistoryRecycle(ctx, target)
}

func (service *Service) Recycle(ctx context.Context, target Target) (Entry, error) {
	if err := target.Validate(); err != nil {
		return Entry{}, err
	}
	return service.repository.RecycleHistory(ctx, target, time.Now().UTC())
}

func (service *Service) Restore(ctx context.Context, target Target) (Entry, error) {
	if err := target.Validate(); err != nil {
		return Entry{}, err
	}
	return service.repository.RestoreHistory(ctx, target)
}

func (service *Service) Page(ctx context.Context, after string) (Page, error) {
	if after != "" {
		if _, _, err := ParseCursor(after); err != nil {
			return Page{}, err
		}
	}
	return service.repository.ListRecycledHistory(ctx, after)
}

func ParseCursor(cursor string) (string, Target, error) {
	parts := strings.Split(cursor, "|")
	if len(parts) != 3 {
		return "", Target{}, ErrInvalid
	}
	target := Target{Kind: parts[1], ID: parts[2]}
	stamp, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil || target.Validate() != nil {
		return "", Target{}, ErrInvalid
	}
	if stamp.IsZero() || stamp.UTC().Format(time.RFC3339Nano) != parts[0] {
		return "", Target{}, ErrInvalid
	}
	return parts[0], target, nil
}
