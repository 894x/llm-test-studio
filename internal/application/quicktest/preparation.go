package quicktest

import (
	"context"
	"net/url"
	"strings"

	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/protocol"
)

func (service *Service) BuildPerformance(
	ctx context.Context,
	command PerformanceCommand,
) (PreparedPerformance, *credentials.Lease, domain.ErrorCode) {
	if service == nil {
		return PreparedPerformance{}, nil, ErrorInvalidRequest
	}
	if command.LoadMode == "" {
		command.LoadMode = domain.LoadFixedConcurrency
	}
	command.ArrivalPattern = normalizedArrivalPattern(command.ArrivalPattern)
	command.WorkloadMode = normalizedWorkloadMode(command.WorkloadMode)
	report := PerformanceReport{
		SchemaVersion: PerformanceSchemaVersion,
		Protocol:      command.Protocol,
		AddressMode:   command.AddressMode,
		ModelID:       command.ModelID,
		ArchiveStatus: PerformanceArchiveNotAttempted,
		Profile:       performanceProfile(command),
		Failures:      []PerformanceFailure{},
		Samples:       []PerformanceSample{},
		Progress:      PerformanceProgress{Phase: PerformancePhaseNotStarted},
	}
	if ctx == nil || !SupportsPerformance(command.Protocol) {
		report.ErrorCode = ErrorInvalidRequest
		return preparedPerformance(report, ""), nil, report.ErrorCode
	}
	if err := ctx.Err(); err != nil {
		return preparedPerformance(report, ""), nil, classifyContext(err)
	}
	if code := service.applySelectedChannel(ctx, &command); code != "" {
		report.ErrorCode = code
		return preparedPerformance(report, ""), nil, report.ErrorCode
	}
	if command.CredentialRunID != "" {
		conflictingCredential := command.ChannelID != "" || command.APIKey != ""
		invalidTask := command.Task == nil || command.AddressMode != AddressModeBaseURL
		if conflictingCredential || invalidTask || service.taskCredential == nil {
			report.ErrorCode = ErrorInvalidRequest
			return preparedPerformance(report, ""), nil, report.ErrorCode
		}
		lease, err := service.taskCredential(ctx, command.CredentialRunID, command.URL, command.Protocol)
		if err != nil {
			report.ErrorCode = ErrorCredentialRequired
			return preparedPerformance(report, ""), nil, report.ErrorCode
		}
		secret, err := lease.Bytes()
		_ = lease.Close()
		if err != nil {
			report.ErrorCode = ErrorCredentialRequired
			return preparedPerformance(report, ""), nil, report.ErrorCode
		}
		command.APIKey = string(secret)
		clear(secret)
	}
	report.AddressMode = command.AddressMode
	if command.Task != nil {
		if service.taskPath == nil || command.AddressMode != AddressModeBaseURL {
			report.ErrorCode = ErrorInvalidRequest
			return preparedPerformance(report, ""), nil, report.ErrorCode
		}
		path, err := service.taskPath(ctx, *command.Task, command.ModelID, command.Protocol)
		parsed, parseErr := url.Parse(path)
		if err != nil || parseErr != nil {
			report.ErrorCode = ErrorInvalidRequest
			return preparedPerformance(report, ""), nil, report.ErrorCode
		}
		externalPath := parsed.IsAbs() || parsed.Host != ""
		pathHasExtraParts := parsed.RawQuery != "" || parsed.Fragment != ""
		wrongRequestPath := !strings.HasPrefix(path, "/") || path != performancePath(command.Protocol)
		if externalPath || pathHasExtraParts || wrongRequestPath {
			report.ErrorCode = ErrorInvalidRequest
			return preparedPerformance(report, ""), nil, report.ErrorCode
		}
		descriptor, _ := protocol.Lookup(string(command.Protocol))
		resolved, resolveErr := protocol.ResolveAddress(command.URL, path, descriptor.RequestPaths)
		if resolveErr != nil {
			report.ErrorCode = ErrorInvalidRequest
			return preparedPerformance(report, ""), nil, report.ErrorCode
		}
		command.URL = resolved.Endpoint
		command.AddressMode = AddressModeFullURL
	}
	address, code := normalizeAddress(command.Protocol, command.AddressMode, command.URL)
	report.BaseURL, report.Endpoint = address.baseURL, address.endpoint
	if code != "" {
		report.ErrorCode = code
		return preparedPerformance(report, ""), nil, report.ErrorCode
	}
	if strings.TrimSpace(command.APIKey) == "" {
		report.ErrorCode = ErrorCredentialRequired
		return preparedPerformance(report, ""), nil, report.ErrorCode
	}
	validAPIKey := command.APIKey == strings.TrimSpace(command.APIKey)
	validModelID := strings.TrimSpace(command.ModelID) != "" && command.ModelID == strings.TrimSpace(command.ModelID)
	if !validAPIKey || !validModelID {
		report.ErrorCode = ErrorInvalidRequest
		return preparedPerformance(report, ""), nil, report.ErrorCode
	}

	if _, _, err := preparePerformanceProfile(report.Profile); err != nil {
		return preparedPerformance(report, ""), nil, ErrorInvalidRequest
	}
	reportID, err := service.idFactory(service.clock.Now().UTC())
	if err != nil || !domain.IsUUID(reportID) {
		return preparedPerformance(report, ""), nil, ErrorInvalidRequest
	}
	secret := []byte(command.APIKey)
	lease, err := credentials.NewTemporaryLease(secret)
	clear(secret)
	command.APIKey = ""
	if err != nil {
		return preparedPerformance(report, ""), nil, ErrorCredentialRequired
	}
	return preparedPerformance(report, reportID), lease, ""
}

func (service *Service) applySelectedChannel(ctx context.Context, command *PerformanceCommand) domain.ErrorCode {
	if command.ChannelID == "" {
		return ""
	}
	validSelection := domain.IsUUID(command.ChannelID) && strings.TrimSpace(command.APIKey) == ""
	if !validSelection || service.channelConnections == nil {
		return ErrorInvalidRequest
	}
	selection := ChannelSelection{ChannelID: command.ChannelID, Model: command.ModelID, Protocol: command.Protocol}
	connection, err := service.channelConnections.Resolve(ctx, selection)
	if err != nil {
		clear(connection.APIKey)
		return ErrorInvalidRequest
	}
	usableConnection := strings.TrimSpace(connection.BaseURL) != "" && len(connection.APIKey) != 0
	if !usableConnection {
		clear(connection.APIKey)
		return ErrorInvalidRequest
	}
	command.AddressMode = AddressModeBaseURL
	command.URL = connection.BaseURL
	command.APIKey = string(connection.APIKey)
	clear(connection.APIKey)
	return ""
}
