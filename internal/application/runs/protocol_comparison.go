package runs

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"

	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/protocol"
)

type ProtocolComparisonCommand struct {
	Plan  *StartCommand     `json:"plan,omitempty"`
	Suite *QuickTaskCommand `json:"suite,omitempty"`
}

func (command ProtocolComparisonCommand) String() string {
	return "ProtocolComparisonCommand{redacted}"
}
func (command ProtocolComparisonCommand) GoString() string { return command.String() }

func (command ProtocolComparisonCommand) Validate() error {
	switch {
	case command.Plan != nil && command.Suite == nil:
		plan := command.Plan
		if domain.IsUUID(plan.PlanID) && plan.Connection != nil && plan.Connection.CredentialRunID == "" {
			return nil
		}
	case command.Suite != nil && command.Plan == nil:
		if domain.IsUUID(command.Suite.SuiteID) && command.Suite.CredentialRunID == "" {
			return nil
		}
	}
	return ErrInvalid
}

func comparisonProtocols() [3]domain.Protocol {
	return [3]domain.Protocol{domain.ProtocolOpenAIChat, domain.ProtocolOpenAIResponses, domain.ProtocolAnthropicMessages}
}

func (service *Preparer) BuildProtocolTargets(
	ctx context.Context,
	command ProtocolComparisonCommand,
	batch domain.EntityMeta,
) ([]PreparedRun, map[string]*credentials.Lease, error) {
	if service == nil || ctx == nil || command.Validate() != nil {
		return nil, nil, ErrInvalid
	}
	allSuites, err := service.repository.ListSuites(ctx)
	if err != nil {
		return nil, nil, err
	}
	suites, err := indexSuites(allSuites)
	if err != nil {
		return nil, nil, err
	}
	var plan domain.Plan
	var anchor domain.Suite
	if command.Plan != nil {
		plan, err = service.repository.GetPlan(ctx, command.Plan.PlanID)
		if err != nil {
			return nil, nil, err
		}
		if !domain.IsComparisonProtocol(plan.Protocol) {
			return nil, nil, ErrNotRunnable
		}
		// A new batch should begin with fresh cache keys. All three runs
		// share its seed and retain their protocol-native request templates.
		digest := sha256.Sum256([]byte(batch.ID))
		plan.Seed = uint64(binary.BigEndian.Uint32(digest[:4]))
	} else {
		anchor = suites[command.Suite.SuiteID]
		if !domain.IsComparisonProtocol(anchor.Protocol) {
			return nil, nil, ErrNotRunnable
		}
	}

	prepared := make([]PreparedRun, 0, 3)
	leases := make(map[string]*credentials.Lease, 3)
	transferred := false
	defer func() {
		if !transferred {
			for _, lease := range leases {
				_ = lease.Close()
			}
		}
	}()
	for _, protocol := range comparisonProtocols() {
		var input PreparedRun
		var lease *credentials.Lease
		if command.Plan != nil {
			input, lease, err = service.buildProtocolPlan(ctx, *command.Plan, plan, protocol, suites)
		} else {
			input, lease, err = service.buildProtocolSuite(ctx, *command.Suite, anchor, protocol, suites)
		}
		if err != nil {
			_ = lease.Close()
			return nil, nil, fmt.Errorf("%s: %w", protocol, err)
		}
		if lease == nil || leases[input.Meta.ID] != nil {
			_ = lease.Close()
			return nil, nil, ErrNotRunnable
		}
		leases[input.Meta.ID] = lease
		input.Snapshot.Channel.BaseURL = protocolComparisonBaseURL(input.Snapshot.Channel.BaseURL)
		if command.Suite != nil {
			// Standalone quick tasks use a plan ID equal to their Run ID.
			// A batch owns one synthetic plan and retains its suite provenance
			// on the comparison, with independent Run identities.
			input.Snapshot.QuickTask = nil
			input.PlanID = batch.ID
			input.Snapshot.Plan = domain.EntityRevisionRef{ID: batch.ID, Revision: batch.Revision}
			input.Snapshot.PlanDocument.EntityMeta = batch
			input.Snapshot.PlanDocument.Name = anchor.Name
			input.Snapshot.PlanDocument.Entries[0].EntryID = batch.ID
			input.Snapshot.Entries[0].EntryID = batch.ID
			input.Snapshot.Entries[0].Name = anchor.Name
		}
		if len(prepared) > 0 {
			first := prepared[0].Snapshot
			input.Snapshot.Model.EntityRevisionRef = first.Model.EntityRevisionRef
			input.Snapshot.Mapping.ModelID = first.Model.ID
		}
		prepared = append(prepared, input)
	}
	transferred = true
	return prepared, leases, nil
}

func (service *Preparer) buildProtocolPlan(
	ctx context.Context,
	command StartCommand,
	plan domain.Plan,
	protocol domain.Protocol,
	suites map[string]domain.Suite,
) (PreparedRun, *credentials.Lease, error) {
	projected, err := projectProtocolPlan(plan, protocol, suites)
	if err != nil {
		return PreparedRun{}, nil, err
	}
	connection := *command.Connection
	connection.Protocol = protocol
	command.Connection = &connection
	return service.buildTarget(ctx, command, &projected)
}

func projectProtocolPlan(
	plan domain.Plan,
	protocol domain.Protocol,
	suites map[string]domain.Suite,
) (domain.Plan, error) {
	plan.Protocol = protocol
	plan.Entries = append([]domain.PlanEntry{}, plan.Entries...)
	for index, entry := range plan.Entries {
		if entry.TargetKind != domain.PlanTargetSuite {
			continue
		}
		source, found := suites[entry.TargetID]
		if !found {
			return domain.Plan{}, ErrNotRunnable
		}
		target, err := protocolSuite(source, protocol, suites)
		if err != nil {
			return domain.Plan{}, err
		}
		plan.Entries[index].TargetID = target.ID
	}
	return plan, nil
}

func (service *Preparer) buildProtocolSuite(
	ctx context.Context,
	command QuickTaskCommand,
	anchor domain.Suite,
	protocol domain.Protocol,
	suites map[string]domain.Suite,
) (PreparedRun, *credentials.Lease, error) {
	suite, err := protocolSuite(anchor, protocol, suites)
	if err != nil {
		return PreparedRun{}, nil, err
	}
	if suite.ID != command.SuiteID {
		command.SourceRunID = ""
	}
	command.SuiteID = suite.ID
	return service.BuildQuickTask(ctx, command)
}

// A saved channel or a temporary connection can contain a complete native
// endpoint. Preserve its version and proxy prefix when switching protocols.
func protocolComparisonBaseURL(address string) string {
	parsed, err := url.Parse(address)
	if err != nil {
		return address
	}
	fullPath := strings.TrimRight(parsed.EscapedPath(), "/")
	for _, requestPath := range []string{
		protocol.OpenAIChatPath, protocol.OpenAIResponsesPath, protocol.AnthropicMessagesPath,
	} {
		suffix := strings.TrimPrefix(requestPath, "/v1")
		if strings.HasSuffix(fullPath, suffix) {
			return strings.TrimSuffix(strings.TrimRight(address, "/"), suffix)
		}
	}
	return address
}

// Protocol-prefixed keys are used by the built-in cache suites. Other suites
// can opt into the same stable key across their protocol-specific documents.
func protocolSuite(
	source domain.Suite,
	protocol domain.Protocol,
	suites map[string]domain.Suite,
) (domain.Suite, error) {
	if source.Protocol == protocol {
		return source, nil
	}
	key := suiteFamilyKey(source)
	var match domain.Suite
	for _, suite := range suites {
		if suite.Protocol != protocol || suiteFamilyKey(suite) != key {
			continue
		}
		if match.ID != "" {
			return domain.Suite{}, fmt.Errorf("%w: ambiguous suite %s", ErrNotRunnable, source.Key)
		}
		match = suite
	}
	if match.ID == "" {
		return domain.Suite{}, fmt.Errorf("%w: suite %s has no %s definition", ErrNotRunnable, source.Key, protocol)
	}
	return match, nil
}

func suiteFamilyKey(suite domain.Suite) string {
	return strings.TrimPrefix(suite.Key, string(suite.Protocol)+".")
}
