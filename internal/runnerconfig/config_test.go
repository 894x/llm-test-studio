package runnerconfig_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/894x/llm-test-studio/internal/jobcontract"
	"github.com/894x/llm-test-studio/internal/runnerconfig"
)

func configFixture(t *testing.T) runnerconfig.Config {
	t.Helper()
	config, err := runnerconfig.New(bytes.Repeat([]byte("a"), 64))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(config.Clear)
	return config
}

func TestNewConfigurationOwnsStableNodeIdentityAndOnlyHashesNodeToken(t *testing.T) {
	config := configFixture(t)
	if err := config.Validate(); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(bytes.Repeat([]byte("a"), 64))
	if config.TokenSHA256 != hex.EncodeToString(digest[:]) {
		t.Fatal("configuration retained the token instead of its digest")
	}
	document, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(document, bytes.Repeat([]byte("a"), 64)) {
		t.Fatal("serialized private configuration contains the node token")
	}
	decoded, err := runnerconfig.Decode(bytes.NewReader(document))
	if err != nil {
		t.Fatal(err)
	}
	defer decoded.Clear()
	if !reflect.DeepEqual(config, decoded) {
		t.Fatal("load changed installation identity, authorization or resource policy")
	}
	printed := fmt.Sprintf(
		"%v %+v %#v",
		config,
		config,
		config,
	)
	leaked := strings.Contains(printed, config.TokenSHA256) ||
		strings.Contains(printed, hex.EncodeToString(config.CommitmentKey))
	if leaked {
		t.Fatal("diagnostics disclose private configuration")
	}
	config.Clear()
	if bytes.Equal(config.CommitmentKey, decoded.CommitmentKey) {
		t.Fatal("decoded HMAC key aliases the cleared caller value")
	}
}

func TestConfigurationRejectsUnsupportedOrInvalidDocumentsWithoutChangingInput(t *testing.T) {
	base := configFixture(t)
	for _, change := range []func(*runnerconfig.Config){
		func(config *runnerconfig.Config) { config.Contract = 0 },
		func(config *runnerconfig.Config) { config.Contract++ },
		func(config *runnerconfig.Config) { config.NodeID = "invalid" },
		func(config *runnerconfig.Config) { config.InstallationID = "invalid" },
		func(config *runnerconfig.Config) { config.ListenPort = 1023 },
		func(config *runnerconfig.Config) { config.TokenSHA256 = strings.Repeat("0", 64) },
		func(config *runnerconfig.Config) { config.TokenSHA256 = strings.Repeat("A", 64) },
		func(config *runnerconfig.Config) { config.CommitmentKey = []byte{} },
		func(config *runnerconfig.Config) { config.CommitmentKey = make([]byte, 32) },
		func(config *runnerconfig.Config) { config.Budget.MaxActiveJobs = 0 },
		func(config *runnerconfig.Config) { config.Budget.ExecutionTimeoutNS = -1 },
		func(config *runnerconfig.Config) { config.MaxStorageBytes = 512<<20 - 1 },
		func(config *runnerconfig.Config) { config.MaxStorageBytes = 1<<40 + 1 },
		func(config *runnerconfig.Config) { config.ReservationBytes = 64<<20 - 1 },
		func(config *runnerconfig.Config) { config.ReservationBytes = config.MaxStorageBytes + 1 },
		func(config *runnerconfig.Config) { config.MinFreeBytes = 1<<40 + 1 },
	} {
		config := base
		change(&config)
		document, _ := json.Marshal(config)
		before := bytes.Clone(document)
		decoded, err := runnerconfig.Decode(bytes.NewReader(document))
		decoded.Clear()
		if err == nil || !bytes.Equal(document, before) {
			t.Fatal("invalid config was accepted or its input changed")
		}
		if config.Contract != jobcontract.CurrentContract && !errors.Is(err, jobcontract.ErrUnsupported) {
			t.Fatal("unsupported contract did not produce an explicit upgrade error")
		}
	}
	valid, _ := json.Marshal(base)
	for _, document := range [][]byte{
		nil, []byte(`null`), []byte(`[]`), []byte(`{`), append(bytes.Clone(valid), []byte(` {}`)...),
		bytes.Replace(
			valid,
			[]byte(`"contract":1`),
			[]byte(`"contract":1,"unknown":1`),
			1,
		),
		bytes.Replace(
			valid,
			[]byte(`"contract":1`),
			[]byte(`"Contract":1`),
			1,
		),
		bytes.Replace(
			valid,
			[]byte(`"contract":1`),
			[]byte(`"contract":1,"contract":1`),
			1,
		),
		bytes.Replace(
			valid,
			[]byte(`"min_free_bytes":536870912,`),
			nil,
			1,
		),
		bytes.Replace(
			valid,
			[]byte(`"min_free_bytes":536870912`),
			[]byte(`"min_free_bytes":null`),
			1,
		),
		bytes.Replace(
			valid,
			[]byte(`"max_queued_jobs":16`),
			[]byte(`"max_queued_jobs":null`),
			1,
		),
		bytes.Replace(
			valid,
			[]byte(`"max_queued_jobs":16`),
			[]byte(`"max_queued_jobs":16,"max_queued_jobs":16`),
			1,
		),
		bytes.Repeat([]byte(" "), runnerconfig.MaxConfigBytes+1),
	} {
		decoded, err := runnerconfig.Decode(bytes.NewReader(document))
		decoded.Clear()
		if err == nil {
			t.Fatal("invalid document shape was accepted")
		}
	}
	keyJSON, _ := json.Marshal(base.CommitmentKey)
	keyNumbers := make([]int, 32)
	for index := range keyNumbers {
		keyNumbers[index] = 1
	}
	arrayKey, _ := json.Marshal(keyNumbers)
	if decoded, err := runnerconfig.Decode(bytes.NewReader(bytes.Replace(
		valid,
		keyJSON,
		arrayKey,
		1,
	))); err == nil {
		decoded.Clear()
		t.Fatal("numeric key array was accepted as another private format")
	}
}

func TestConfigurationTokenAndDocumentBoundaries(t *testing.T) {
	for _, length := range []int{31, 32, 128, 129} {
		config, err := runnerconfig.New(bytes.Repeat([]byte("t"), length))
		config.Clear()
		if (err == nil) != (length >= 32 && length <= 128) {
			t.Fatalf("token length boundary %d changed", length)
		}
	}
	for _, suffix := range []string{" ", "\r", "\n", "\x00", ":"} {
		if config, err := runnerconfig.New([]byte(strings.Repeat("t", 63) + suffix)); err == nil {
			config.Clear()
			t.Fatal("invalid bearer token characters were accepted")
		}
	}
	config := configFixture(t)
	config.ListenPort = 65535
	config.MaxStorageBytes, config.MinFreeBytes, config.ReservationBytes = 1<<40, 1<<40, 1<<40
	if err := config.Validate(); err != nil {
		t.Fatal("exact configuration upper boundaries were rejected")
	}
	document, _ := json.Marshal(config)
	document = append(document, bytes.Repeat([]byte(" "), runnerconfig.MaxConfigBytes-len(document))...)
	decoded, err := runnerconfig.Decode(bytes.NewReader(document))
	if err != nil {
		t.Fatalf("exact configuration byte limit rejected: %v", err)
	}
	decoded.Clear()
}
