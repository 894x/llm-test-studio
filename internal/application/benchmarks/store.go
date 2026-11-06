package benchmarks

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/fileconfig"
)

const pageSize = 25
const maxDocumentBytes = 512 * 1024

type store struct{ directory string }

func validRunID(id string) bool {
	if len(id) != 57 || id[20] != '-' || !domain.IsUUID(id[21:]) {
		return false
	}
	_, err := strconv.ParseInt(strings.TrimLeft(id[:20], "0"), 10, 64)
	return err == nil && !strings.ContainsAny(id[:20], "+- ")
}

func newRunID(now time.Time, id string) string { return fmt.Sprintf("%020d-%s", now.UnixNano(), id) }

func (s store) runPath(id string) string { return filepath.Join(s.directory, "runs", id, "run.json") }

func (s store) writeRun(ctx context.Context, run Run) error {
	if validateRun(run) != nil {
		return ErrFormat
	}
	return writeJSON(ctx, s.runPath(run.ID), run)
}

func (s store) readRun(id string) (Run, error) {
	if !validRunID(id) {
		return Run{}, ErrInvalid
	}
	var run Run
	if err := readJSON(s.runPath(id), &run); err != nil {
		return Run{}, err
	}
	if validateRun(run) != nil || run.ID != id {
		return Run{}, ErrFormat
	}
	return run, nil
}

func (s store) list(after string) (Page, error) {
	page := Page{SchemaVersion: 1, Runs: []Run{}}
	if after != "" && !validRunID(after) {
		return page, ErrInvalid
	}
	entries, err := os.ReadDir(filepath.Join(s.directory, "runs"))
	if errors.Is(err, os.ErrNotExist) {
		return page, nil
	}
	if err != nil {
		return page, err
	}
	ids := []string{}
	for _, entry := range entries {
		if entry.IsDir() && validRunID(entry.Name()) && (after == "" || entry.Name() < after) {
			ids = append(ids, entry.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(ids)))
	for index, id := range ids {
		if index == pageSize {
			page.NextCursor = ids[index-1]
			break
		}
		run, err := s.readRun(id)
		if err != nil {
			return page, err
		}
		page.Runs = append(page.Runs, run)
	}
	return page, nil
}

func sampleKey(sample Sample) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d", sample.ID, sample.Epoch)))
	return hex.EncodeToString(sum[:])
}

func (s store) sampleDirectory(runID, reportID string) string {
	return filepath.Join(s.directory, "runs", runID, reportID, "samples")
}

func (s store) writeSample(ctx context.Context, runID, reportID string, sample Sample) error {
	if !validRunID(runID) || !domain.IsUUID(reportID) || validateSample(sample) != nil {
		return ErrFormat
	}
	return writeJSON(ctx, filepath.Join(s.sampleDirectory(runID, reportID), sampleKey(sample)+".json"), struct {
		SchemaVersion int    `json:"schema_version"`
		Sample        Sample `json:"sample"`
	}{SchemaVersion: 1, Sample: sample})
}

func (s store) samples(runID, reportID, after string) ([]Sample, string, error) {
	samples := []Sample{}
	if after != "" && !validSampleCursor(after) {
		return samples, "", ErrInvalid
	}
	directory := s.sampleDirectory(runID, reportID)
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return samples, "", nil
	}
	if err != nil {
		return samples, "", err
	}
	ids := []string{}
	for _, entry := range entries {
		id := strings.TrimSuffix(entry.Name(), ".json")
		if !entry.IsDir() && validSampleCursor(id) && id > after {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for index, id := range ids {
		if index == 10 {
			return samples, ids[index-1], nil
		}
		var document struct {
			SchemaVersion int    `json:"schema_version"`
			Sample        Sample `json:"sample"`
		}
		if err := readJSON(filepath.Join(directory, id+".json"), &document); err != nil {
			return samples, "", err
		}
		if document.SchemaVersion != 1 || validateSample(document.Sample) != nil || sampleKey(document.Sample) != id {
			return samples, "", ErrFormat
		}
		samples = append(samples, document.Sample)
	}
	return samples, "", nil
}

func validSampleCursor(id string) bool {
	if len(id) != 64 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil && strings.ToLower(id) == id
}

func readJSON(path string, target any) error {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	defer file.Close()
	payload, err := io.ReadAll(io.LimitReader(file, maxDocumentBytes+1))
	if err != nil {
		return err
	}
	if len(payload) > maxDocumentBytes {
		return ErrFormat
	}
	if !completeShape(payload, reflect.TypeOf(target).Elem()) {
		return ErrFormat
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil {
		return ErrFormat
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return ErrFormat
	}
	return nil
}

// Version-one persisted documents require every declared field, including zero
// values. Missing fields do not select guessed defaults at a storage boundary.
func completeShape(payload []byte, kind reflect.Type) bool {
	if kind.Kind() == reflect.Pointer {
		return bytes.Equal(bytes.TrimSpace(payload), []byte("null")) || completeShape(payload, kind.Elem())
	}
	switch kind.Kind() {
	case reflect.Struct:
		var fields map[string]json.RawMessage
		if json.Unmarshal(payload, &fields) != nil || len(fields) != kind.NumField() {
			return false
		}
		for i := 0; i < kind.NumField(); i++ {
			field := kind.Field(i)
			name := strings.Split(field.Tag.Get("json"), ",")[0]
			value, exists := fields[name]
			if !exists || !completeShape(value, field.Type) {
				return false
			}
		}
	case reflect.Slice:
		var values []json.RawMessage
		if json.Unmarshal(payload, &values) != nil || values == nil {
			return false
		}
		for _, value := range values {
			if !completeShape(value, kind.Elem()) {
				return false
			}
		}
	}
	return true
}

func writeJSON(ctx context.Context, path string, value any) error {
	payload, err := json.Marshal(value)
	if err != nil || len(payload) > maxDocumentBytes {
		return ErrFormat
	}
	return fileconfig.WriteAtomically(ctx, path, payload)
}

func validStatus(value string) bool {
	return Active(value) || value == "completed" || value == "failed" || value == "cancelled" || value == "interrupted"
}

func validateRun(run Run) error {
	validTarget := supportedProtocol(run.Target.Protocol) && safeText(run.Target.Model, 256, false) && ValidateBaseURL(run.Target.BaseURL) == nil
	validDatasets := len(run.Datasets) > 0 && len(run.Datasets) <= 16
	if run.SchemaVersion != 1 || !validRunID(run.ID) || !validStatus(run.Status) || !validTarget || !validDatasets || run.Options.Validate() != nil {
		return ErrFormat
	}
	if _, err := time.Parse(time.RFC3339Nano, run.CreatedAt); err != nil {
		return ErrFormat
	}
	if run.FinishedAt != "" {
		if _, err := time.Parse(time.RFC3339Nano, run.FinishedAt); err != nil {
			return ErrFormat
		}
	}
	seen := map[string]bool{}
	for _, dataset := range run.Datasets {
		validCounts := dataset.Total >= 0 && dataset.Total <= 100000 && dataset.Completed >= 0 && dataset.Completed <= dataset.Total
		validFailures := dataset.Failed >= 0 && dataset.Failed <= dataset.Completed && dataset.InFlight >= 0 && dataset.InFlight <= 64
		if !domain.IsUUID(dataset.ReportID) || seen[dataset.ReportID] || !validStatus(dataset.Status) || !validCounts || !validFailures {
			return ErrFormat
		}
		seen[dataset.ReportID] = true
		if len(dataset.Metrics) > 128 || dataset.Metrics == nil || dataset.Dataset.ID == "" {
			return ErrFormat
		}
		for _, metric := range dataset.Metrics {
			if !safeText(metric.Name, 256, false) || !safeText(metric.Scorer, 256, false) {
				return ErrFormat
			}
		}
	}
	return nil
}

func validateSample(sample Sample) error {
	validID := safeText(sample.ID, 256, false) && sample.Epoch >= 1 && sample.Epoch <= 32
	validState := sample.Status == "completed" || sample.Status == "error"
	validUsage := sample.InputTokens >= 0 && sample.OutputTokens >= 0 && sample.DurationMS >= 0
	if !validID || !validState || !validUsage || sample.Scores == nil || len(sample.Scores) > 32 {
		return ErrFormat
	}
	values := []string{sample.Input, sample.Target, sample.Output}
	for _, score := range sample.Scores {
		values = append(values, score.Name, score.Value, score.Answer, score.Explanation)
	}
	for _, value := range values {
		if len(value) > 48000 || strings.ContainsRune(value, 0) {
			return ErrFormat
		}
	}
	return nil
}
