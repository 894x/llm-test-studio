package remotenodes

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	remotessh "github.com/894x/llm-test-studio/internal/remote/ssh"
	"github.com/894x/llm-test-studio/internal/runnerinstall"
)

func TestPrivateKeyAndPassphraseUseSeparateCredentialPurposes(t *testing.T) {
	fixture := newNodeFixture(t)
	node := fixture.save(t, passwordCommand(t))
	command := editCommand(node)
	command.Authentication = AuthenticationPrivateKey
	command.Secrets = SecretUpdate{
		PrivateKey: temporarySecret(t, "fixture-private-key"), Passphrase: temporarySecret(t, "fixture-key-passphrase"),
	}
	node = fixture.save(t, command)
	if node.Authentication.PasswordRef != "" {
		t.Fatal("authentication change retained password reference")
	}
	key, _ := credentials.ParseStoreRef(node.Authentication.PrivateKeyRef)
	phrase, _ := credentials.ParseStoreRef(node.Authentication.PassphraseRef)
	if key.Purpose() != domain.CredentialSSHPrivateKey || phrase.Purpose() != domain.CredentialSSHPassphrase {
		t.Fatal("private key and passphrase purposes were mixed")
	}
	ctx := context.Background()
	connection, err := fixture.service.AcquireSSH(ctx, node.ID, node.Revision)
	if err != nil || len(connection.leases) != 2 {
		t.Fatalf("private key lease: %v", err)
	}
	_ = connection.Close()
	command = editCommand(node)
	command.Secrets.RemovePassphrase = true
	node = fixture.save(t, command)
	if node.Authentication.PassphraseRef != "" || node.Authentication.PrivateKeyRef != key.Value() {
		t.Fatal("removing passphrase changed private key")
	}
	if err := fixture.service.Cleanup(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.keys.Get(ctx, phrase); !errors.Is(err, credentials.ErrNotFound) {
		t.Fatal("removed passphrase not collected")
	}
	document, _ := os.ReadFile(fixture.store.path)
	containsKey := strings.Contains(string(document), "fixture-private-key")
	containsPhrase := strings.Contains(string(document), "fixture-key-passphrase")
	if containsKey || containsPhrase {
		t.Fatal("private key material persisted in metadata")
	}
}

func TestObservationsRejectStaleInvalidIdentityAndPlatform(t *testing.T) {
	fixture := newNodeFixture(t)
	node := fixture.bind(t, fixture.save(t, passwordCommand(t)))
	ctx := context.Background()
	observation := readyObservation(node, fixture.now)
	if err := fixture.service.RecordObservation(
		ctx,
		node.ID,
		node.Revision,
		observation,
	); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(fixture.store.path)
	for _, mutate := range []func(*Observation){
		func(value *Observation) { value.CheckedAt = value.CheckedAt.Add(-time.Nanosecond) },
		func(value *Observation) { value.Runner.NodeInstallationID = fixtureID(t) },
		func(value *Observation) { value.Runner.OS = "windows" },
		func(value *Observation) { value.Runner.Arch = "386" },
		func(value *Observation) { value.Runner = nil },
	} {
		candidate := readyObservation(node, fixture.now)
		mutate(&candidate)
		if err := fixture.service.RecordObservation(
			ctx,
			node.ID,
			node.Revision,
			candidate,
		); err == nil {
			t.Fatal("invalid observation accepted")
		}
		after, _ := os.ReadFile(fixture.store.path)
		if string(before) != string(after) {
			t.Fatal("rejected observation mutated file")
		}
	}
	snapshot, err := fixture.service.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	document, _ := json.Marshal(snapshot)
	if strings.Contains(string(document), "token_sha256") || strings.Contains(string(document), "token_ref") {
		t.Fatal("snapshot exposed pairing material")
	}
}

func TestNodeAndCredentialRegistryBounds(t *testing.T) {
	fixture := newNodeFixture(t)
	first := fixture.save(t, passwordCommand(t))
	config, err := fixture.store.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for len(config.Nodes) < MaxNodes {
		node := first
		node.ID = fixtureID(t)
		ref, err := credentials.NewStoreRef(domain.CredentialSSHPassword, fixtureID(t))
		if err != nil {
			t.Fatal(err)
		}
		node.Authentication.PasswordRef = ref.Value()
		config.Nodes = append(config.Nodes, node)
		config.CredentialRefs = append(config.CredentialRefs, ref.Value())
	}
	slices.Sort(config.CredentialRefs)
	if err := fixture.store.write(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(fixture.store.path)
	if _, err := fixture.service.Save(context.Background(), passwordCommand(t)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("65th node accepted: %v", err)
	}
	after, _ := os.ReadFile(fixture.store.path)
	if string(before) != string(after) {
		t.Fatal("node limit rejection mutated configuration")
	}
	for len(config.CredentialRefs) < MaxCredentialRefs+1 {
		ref, _ := credentials.NewStoreRef(domain.CredentialSSHPassword, fixtureID(t))
		config.CredentialRefs = append(config.CredentialRefs, ref.Value())
	}
	slices.Sort(config.CredentialRefs)
	if !errors.Is(config.Validate(), ErrInvalid) {
		t.Fatal("unbounded credential registry accepted")
	}
	config = emptyConfiguration()
	config.Default = Selection{Revision: 1, Kind: jobcontract.TargetRemote, NodeID: fixtureID(t)}
	if !errors.Is(config.Validate(), ErrNotFound) {
		t.Fatal("default accepted a missing node")
	}
}

func TestInstallationRetryCannotChangeRequestUnderSameOperationID(t *testing.T) {
	fixture := newNodeFixture(t)
	node := fixture.save(t, passwordCommand(t))
	ctx := context.Background()
	attempt := Installation{Request: runnerinstall.Request{
		Contract: jobcontract.CurrentContract, OperationID: fixtureID(t),
		ArtifactSHA256: strings.Repeat("c", 64), ListenPort: 44917,
	}, Release: "fixture", Arch: "amd64", Stage: "uploading"}
	attempt.TemporaryDirectory = remotessh.BootstrapDirectory(attempt.Request.OperationID)
	command := InstallationCommand{ID: node.ID, ExpectedRevision: node.Revision, Installation: attempt}
	if err := fixture.service.RecordInstallation(ctx, command); err != nil {
		t.Fatal(err)
	}
	command.ExpectedOperationID = attempt.Request.OperationID
	command.Installation.Stage = "checking"
	if err := fixture.service.RecordInstallation(ctx, command); err != nil {
		t.Fatal(err)
	}
	command.Installation.Request.ArtifactSHA256 = strings.Repeat("d", 64)
	if err := fixture.service.RecordInstallation(ctx, command); !errors.Is(err, ErrConflict) {
		t.Fatal("uncertain retry accepted a different artifact")
	}
	for _, mutate := range []func(*Installation){
		func(value *Installation) { value.TemporaryDirectory = "" },
		func(value *Installation) { value.TemporaryDirectory = remotessh.BootstrapDirectory(fixtureID(t)) },
		func(value *Installation) { value.Request.OperationID = fixtureID(t) },
	} {
		command.Installation = attempt
		mutate(&command.Installation)
		if err := fixture.service.RecordInstallation(ctx, command); !errors.Is(err, ErrConflict) {
			t.Fatal("installation retry replaced its original operation or bootstrap path")
		}
	}
}

func TestConfigurationRejectsSupersededOrMismatchedBootstrapPathsWithoutMutation(t *testing.T) {
	fixture := newNodeFixture(t)
	fixture.save(t, passwordCommand(t))
	config, err := fixture.store.Read(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	request := runnerinstall.Request{Contract: jobcontract.CurrentContract, OperationID: fixtureID(t),
		ArtifactSHA256: strings.Repeat("c", 64), ListenPort: 44917}
	for _, directory := range []string{"", "/var/tmp/llm-test-runner-bootstrap.ABCDE12345",
		remotessh.BootstrapDirectory(fixtureID(t))} {
		config.Nodes[0].Installation = &Installation{Request: request, Release: "fixture", Arch: "amd64",
			Stage: "uploading", TemporaryDirectory: directory, CleanupPending: true}
		document, err := json.Marshal(config)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fixture.store.path, document, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.store.Read(t.Context()); !errors.Is(err, ErrInvalid) {
			t.Fatal("configuration accepted a missing, superseded or mismatched operation path")
		}
		after, err := os.ReadFile(fixture.store.path)
		if err != nil || string(after) != string(document) {
			t.Fatal("rejected historical data was changed during configuration read")
		}
	}
}
