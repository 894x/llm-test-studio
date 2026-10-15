package client

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	remotessh "github.com/894x/llm-test-studio/internal/remote/ssh"
)

func (client *Client) ListJobs(ctx context.Context, after uint64, limit uint32) (jobhost.JobPage, error) {
	valid := after <= math.MaxInt64 && limit >= 1 && limit <= 200
	if !valid {
		return jobhost.JobPage{}, ErrInvalid
	}
	route := "/jobs?after=" + strconv.FormatUint(after, 10) + "&limit=" + strconv.FormatUint(uint64(limit), 10)
	raw, err := client.json(ctx, remotessh.Request{Method: http.MethodGet, Route: route}, jobcontract.MaxEventPageBytes)
	if err != nil {
		return jobhost.JobPage{}, err
	}
	_, err = object(raw, []string{"contract", "jobs", "next_cursor", "has_more"}, []string{})
	if err != nil {
		return jobhost.JobPage{}, err
	}
	var wire struct {
		Contract   int             `json:"contract"`
		Jobs       json.RawMessage `json:"jobs"`
		NextCursor string          `json:"next_cursor"`
		HasMore    bool            `json:"has_more"`
	}
	if json.Unmarshal(raw, &wire) != nil {
		return jobhost.JobPage{}, ErrIntegrity
	}
	if wire.Contract != jobcontract.CurrentContract {
		return jobhost.JobPage{}, jobcontract.ErrUnsupported
	}
	jobs, err := array(wire.Jobs, int(limit))
	if err != nil {
		return jobhost.JobPage{}, err
	}
	cursor, err := strconv.ParseUint(wire.NextCursor, 10, 63)
	validCursor := err == nil && strconv.FormatUint(cursor, 10) == wire.NextCursor && cursor >= after
	validEmpty := len(jobs) > 0 || (!wire.HasMore && cursor == after)
	advanced := len(jobs) == 0 || cursor > after
	if !validCursor || !validEmpty || !advanced {
		return jobhost.JobPage{}, ErrIntegrity
	}
	page := jobhost.JobPage{Contract: wire.Contract, Jobs: []jobcontract.Snapshot{},
		NextCursor: wire.NextCursor, HasMore: wire.HasMore}
	seen := map[string]bool{}
	for _, raw := range jobs {
		snapshot, err := jobcontract.DecodeSnapshot(bytes.NewReader(raw))
		if err != nil || !client.owns(snapshot) || seen[snapshot.Receipt.JobID] {
			return jobhost.JobPage{}, ErrIntegrity
		}
		seen[snapshot.Receipt.JobID] = true
		page.Jobs = append(page.Jobs, snapshot)
	}
	return page, nil
}

type EventQuery struct {
	JobID         string
	AfterSequence uint64
	Limit         uint32
	Wait          time.Duration
}

func (client *Client) ReadEvents(ctx context.Context, query EventQuery) (jobhost.EventPage, error) {
	id, after, limit, wait := query.JobID, query.AfterSequence, query.Limit, query.Wait
	validCursor := domain.IsUUID(id) && after <= math.MaxInt64 && limit >= 1 && limit <= 200
	validWait := wait >= 0 && wait <= 20*time.Second && wait%time.Millisecond == 0
	if !validCursor || !validWait {
		return jobhost.EventPage{}, ErrInvalid
	}
	route := "/jobs/" + id + "/events?after_sequence=" + strconv.FormatUint(after, 10) +
		"&limit=" + strconv.FormatUint(uint64(limit), 10) + "&wait_ms=" + strconv.FormatInt(wait.Milliseconds(), 10)
	raw, err := client.json(ctx, remotessh.Request{Method: http.MethodGet, Route: route}, jobcontract.MaxEventPageBytes)
	if err != nil {
		return jobhost.EventPage{}, err
	}
	if _, err := object(raw, []string{"events", "last_sequence", "has_more"}, []string{}); err != nil {
		return jobhost.EventPage{}, err
	}
	var wire struct {
		Events       json.RawMessage `json:"events"`
		LastSequence uint64          `json:"last_sequence"`
		HasMore      bool            `json:"has_more"`
	}
	if json.Unmarshal(raw, &wire) != nil {
		return jobhost.EventPage{}, ErrIntegrity
	}
	events, err := array(wire.Events, int(limit))
	if err != nil {
		return jobhost.EventPage{}, err
	}
	validSequence := wire.LastSequence >= after && wire.LastSequence <= math.MaxInt64 &&
		wire.LastSequence-after == uint64(len(events))
	if !validSequence || (wire.HasMore && len(events) == 0) {
		return jobhost.EventPage{}, ErrIntegrity
	}
	page := jobhost.EventPage{Events: []jobcontract.Event{}, LastSequence: wire.LastSequence, HasMore: wire.HasMore}
	for index, raw := range events {
		event, err := jobcontract.DecodeEvent(bytes.NewReader(raw))
		valid := err == nil && event.JobID == id && event.Sequence == after+uint64(index)+1
		if !valid {
			return jobhost.EventPage{}, ErrIntegrity
		}
		page.Events = append(page.Events, event)
	}
	return page, nil
}
