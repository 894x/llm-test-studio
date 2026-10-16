package jobcontract

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

type lifecycleDecodeFixture struct {
	name   string
	raw    []byte
	limit  int
	decode func(io.Reader) error
}

func lifecycleDecodeFixtures(t *testing.T) []lifecycleDecodeFixture {
	t.Helper()
	manifest, holding, command, tombstone := contentReleaseFixture(t)
	ack := HoldingAcknowledgement{
		Contract: CurrentContract, ReceiptID: holding.ReceiptID, JobID: holding.JobID,
		ManifestSHA256: holding.ManifestSHA256, ReceivedAt: tombstone.ReleasedAt,
	}
	status := ReleaseStatus{
		Contract: CurrentContract, JobID: manifest.JobID, State: ReleaseCompleted,
		CommandID: &command.CommandID, Tombstone: &tombstone, Cleanup: CleanupSummary{Pending: holding.ItemCount},
	}
	manifestRaw, _ := json.Marshal(manifest)
	holdingRaw, _ := json.Marshal(holding)
	ackRaw, _ := json.Marshal(ack)
	commandRaw, _ := json.Marshal(command)
	tombstoneRaw, _ := json.Marshal(tombstone)
	statusRaw, _ := json.Marshal(status)
	return []lifecycleDecodeFixture{
		{name: "manifest", raw: manifestRaw, limit: MaxFinalManifestBytes,
			decode: lifecycleDecoder(DecodeFinalManifest)},
		{name: "holding", raw: holdingRaw, limit: MaxHoldingAttestationBytes,
			decode: lifecycleDecoder(DecodeHoldingAttestation)},
		{name: "acknowledgement", raw: ackRaw, limit: MaxHoldingAcknowledgementBytes,
			decode: lifecycleDecoder(DecodeHoldingAcknowledgement)},
		{name: "release command", raw: commandRaw, limit: MaxReleaseCommandBytes,
			decode: lifecycleDecoder(DecodeReleaseCommand)},
		{name: "tombstone", raw: tombstoneRaw, limit: MaxReleaseTombstoneBytes,
			decode: lifecycleDecoder(DecodeReleaseTombstone)},
		{name: "status", raw: statusRaw, limit: MaxReleaseStatusBytes,
			decode: lifecycleDecoder(DecodeReleaseStatus)},
	}
}

func lifecycleDecoder[T contentLifecycleWire](decode func(io.Reader) (T, error)) func(io.Reader) error {
	return func(reader io.Reader) error {
		_, err := decode(reader)
		return err
	}
}

func TestContentLifecycleDecodersRequireAllExactRootFields(t *testing.T) {
	for _, fixture := range lifecycleDecodeFixtures(t) {
		t.Run(fixture.name, func(t *testing.T) {
			if err := fixture.decode(bytes.NewReader(fixture.raw)); err != nil {
				t.Fatal("valid current document rejected", err)
			}
			assertLifecycleObjectFields(t, fixture, []string{}, []string{})
			for _, raw := range [][]byte{
				[]byte("null"), []byte("[]"), append(bytes.Clone(fixture.raw), []byte(" {}")...),
				[]byte(strings.Replace(string(fixture.raw), `"contract":1`, `"contract":1,"contract":1`, 1)),
				[]byte(strings.Replace(string(fixture.raw), `"contract":1`, `"contract":1,"contr\u0061ct":1`, 1)),
				[]byte(strings.Replace(string(fixture.raw), `"contract":1`, `"Contract":1`, 1)),
				[]byte(strings.Replace(string(fixture.raw), `"contract":1`, `"contract":1,"private_path":"x"`, 1)),
				[]byte(strings.Replace(string(fixture.raw), `"contract":1`, `"contract":1.0`, 1)),
			} {
				if !errors.Is(fixture.decode(bytes.NewReader(raw)), ErrInvalid) {
					t.Fatal("malformed current envelope accepted")
				}
			}
			unknown := bytes.Replace(fixture.raw, []byte(`"contract":1`), []byte(`"contract":2`), 1)
			if !errors.Is(fixture.decode(bytes.NewReader(unknown)), ErrUnsupported) {
				t.Fatal("unknown contract was defaulted or negotiated")
			}
		})
	}
}

func TestContentLifecycleNestedFieldsAndRequiredNullableKeys(t *testing.T) {
	fixtures := lifecycleDecodeFixtures(t)
	manifest := fixtures[0]
	for _, test := range []struct {
		path     []string
		nullable []string
	}{
		{path: []string{"execution_origin"}, nullable: []string{}},
		{path: []string{"items", "0"}, nullable: []string{"run_id"}},
		{path: []string{"documents", "0"}, nullable: []string{"run_id"}},
		{path: []string{"capture_failures", "0"}, nullable: []string{"run_id"}},
	} {
		assertLifecycleObjectFields(t, manifest, test.path, test.nullable)
	}
	status := fixtures[5]
	assertLifecycleObjectFields(t, status, []string{"cleanup"}, []string{})
	assertLifecycleObjectFields(t, status, []string{"tombstone"}, []string{})
	for _, raw := range []string{
		strings.Replace(string(manifest.raw), `"run_id":null`, `"run_id":null,"run_id":null`, 1),
		strings.Replace(string(manifest.raw), `"run_id":null`, `"run_id":null,"run\u005fid":null`, 1),
		strings.Replace(string(manifest.raw), `"media_type":"image/png"`, `"media_type":"image/png","url":"https://example.test/file"`, 1),
		strings.Replace(string(manifest.raw), `"runner_version":`, `"runner_version":"x","runner_version":`, 1),
	} {
		if !errors.Is(manifest.decode(strings.NewReader(raw)), ErrInvalid) {
			t.Fatal("nested duplicate or unknown metadata accepted")
		}
	}
	none := ReleaseStatus{Contract: CurrentContract, JobID: "11111111-1111-4111-8111-111111111111", State: ReleaseNone}
	raw, _ := json.Marshal(none)
	if _, err := DecodeReleaseStatus(bytes.NewReader(raw)); err != nil {
		t.Fatal("explicit null command/tombstone keys rejected", err)
	}
	fixture := lifecycleDecodeFixture{raw: raw, decode: lifecycleDecoder(DecodeReleaseStatus)}
	assertLifecycleObjectFields(t, fixture, []string{}, []string{"command_id", "tombstone"})
}

func assertLifecycleObjectFields(
	t *testing.T,
	fixture lifecycleDecodeFixture,
	path []string,
	nullable []string,
) {
	t.Helper()
	object := lifecycleObjectAt(t, fixture.raw, path)
	for field := range object {
		for _, mode := range []string{"missing", "null", "wrong_type"} {
			changed := changeLifecycleObject(t, fixture.raw, path, func(fields map[string]json.RawMessage) {
				switch mode {
				case "missing":
					delete(fields, field)
				case "null":
					fields[field] = json.RawMessage("null")
				case "wrong_type":
					fields[field] = json.RawMessage("{}")
				}
			})
			err := fixture.decode(bytes.NewReader(changed))
			validNull := mode == "null" && containsLifecycleField(nullable, field)
			if validNull && err != nil {
				t.Fatalf("required nullable %s.%s rejected: %v", strings.Join(path, "."), field, err)
			}
			if !validNull && !errors.Is(err, ErrInvalid) {
				t.Fatalf("accepted %s.%s/%s: %v", strings.Join(path, "."), field, mode, err)
			}
		}
	}
}

func containsLifecycleField(fields []string, candidate string) bool {
	for _, field := range fields {
		if candidate == field {
			return true
		}
	}
	return false
}

func lifecycleObjectAt(t *testing.T, raw []byte, path []string) map[string]json.RawMessage {
	t.Helper()
	if len(path) != 0 && path[0] == "0" {
		var elements []json.RawMessage
		if err := json.Unmarshal(raw, &elements); err != nil || len(elements) == 0 {
			t.Fatal("invalid array fixture", err)
		}
		return lifecycleObjectAt(t, elements[0], path[1:])
	}
	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	if len(path) != 0 {
		return lifecycleObjectAt(t, fields[path[0]], path[1:])
	}
	return fields
}

func changeLifecycleObject(
	t *testing.T,
	raw []byte,
	path []string,
	change func(map[string]json.RawMessage),
) []byte {
	t.Helper()
	if len(path) != 0 && path[0] == "0" {
		var elements []json.RawMessage
		if err := json.Unmarshal(raw, &elements); err != nil || len(elements) == 0 {
			t.Fatal("invalid array fixture", err)
		}
		elements[0] = changeLifecycleObject(t, elements[0], path[1:], change)
		encoded, _ := json.Marshal(elements)
		return encoded
	}
	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	if len(path) == 0 {
		change(fields)
	} else {
		fields[path[0]] = changeLifecycleObject(t, fields[path[0]], path[1:], change)
	}
	encoded, _ := json.Marshal(fields)
	return encoded
}

func TestContentLifecycleDecodersReadOnlyTheirFixedByteLimit(t *testing.T) {
	for _, fixture := range lifecycleDecodeFixtures(t) {
		t.Run(fixture.name, func(t *testing.T) {
			padded := append(bytes.Clone(fixture.raw), bytes.Repeat([]byte(" "), fixture.limit-len(fixture.raw))...)
			if err := fixture.decode(bytes.NewReader(padded)); err != nil {
				t.Fatal("document at exact byte limit rejected", err)
			}
			if !errors.Is(fixture.decode(bytes.NewReader(append(padded, ' '))), ErrTooLarge) {
				t.Fatal("document beyond byte limit accepted")
			}
			reader := &lifecycleCountingReader{remaining: fixture.limit + 100}
			if !errors.Is(fixture.decode(reader), ErrTooLarge) || reader.read != fixture.limit+1 {
				t.Fatal("decoder exceeded limit+1 or accepted oversized document")
			}
			if !errors.Is(fixture.decode(nil), ErrInvalid) {
				t.Fatal("nil document accepted")
			}
			if !errors.Is(fixture.decode(lifecycleFailedReader{}), io.ErrUnexpectedEOF) {
				t.Fatal("source read failure lost")
			}
		})
	}
}

func TestFinalManifestDecoderRejectsOversizedAndMalformedArrays(t *testing.T) {
	manifest, _, _ := contentManifestFixture(t)
	raw, _ := json.Marshal(manifest)
	for _, test := range []struct {
		field string
		limit int
		raw   []byte
	}{
		{field: "items", limit: MaxManifestItems, raw: mustLifecycleJSON(t, manifest.Items[0])},
		{field: "documents", limit: MaxManifestDocuments, raw: mustLifecycleJSON(t, manifest.Documents[0])},
		{field: "capture_failures", limit: MaxManifestArtifacts, raw: mustLifecycleJSON(t, manifest.CaptureFailures[0])},
	} {
		fields := lifecycleObjectAt(t, raw, []string{})
		prefix := append(bytes.Repeat(append(bytes.Clone(test.raw), ','), test.limit), []byte(`{"malformed":`)...)
		// RawMessage cannot marshal malformed JSON. Assemble its broken tail
		// explicitly and verify rejection independently of the cardinality check.
		delete(fields, test.field)
		other, _ := json.Marshal(fields)
		document := append(bytes.Clone(other[:len(other)-1]), []byte(`,"`+test.field+`":`)...)
		document = append(document, '[')
		document = append(document, prefix...)
		document = append(document, ']')
		document = append(document, '}')
		if _, err := DecodeFinalManifest(bytes.NewReader(document)); !errors.Is(err, ErrInvalid) {
			// The enclosing object decoder must parse the complete JSON value before
			// nested cardinality checks; malformed input remains an explicit reject.
			t.Fatal("malformed oversized array accepted", err)
		}
		validArray := append([]byte{'['}, bytes.Repeat(append(bytes.Clone(test.raw), ','), test.limit)...)
		validArray = append(validArray, test.raw...)
		validArray = append(validArray, ']')
		fields[test.field] = validArray
		validOversize, _ := json.Marshal(fields)
		if _, err := DecodeFinalManifest(bytes.NewReader(validOversize)); !errors.Is(err, ErrTooLarge) {
			t.Fatal("oversized collection not rejected at fixed item bound", err)
		}
	}
}

func mustLifecycleJSON[T ManifestItem | ManifestDocument | CaptureFailure](t *testing.T, value T) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

type lifecycleCountingReader struct{ remaining, read int }

func (reader *lifecycleCountingReader) Read(buffer []byte) (int, error) {
	count := min(len(buffer), reader.remaining)
	for index := range count {
		buffer[index] = ' '
	}
	reader.remaining -= count
	reader.read += count
	if count == 0 {
		return 0, io.EOF
	}
	return count, nil
}

type lifecycleFailedReader struct{}

func (lifecycleFailedReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
