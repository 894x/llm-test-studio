package quicktest

import (
	"context"
	"time"

	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
)

const (
	quickTestChannelID        = "123e4567-e89b-42d3-a456-426614174090"
	quickTestCredentialID     = "123e4567-e89b-42d3-a456-426614174091"
	performanceArchiveTimeout = 5 * time.Second
)

type Service struct {
	*Host
	taskCredential     func(context.Context, string, string, domain.Protocol) (*credentials.Lease, error)
	taskPath           func(context.Context, TaskReference, string, domain.Protocol) (string, error)
	channelConnections ChannelConnectionResolver
	idFactory          PerformanceReportIDFactory
}

func New(dependencies Dependencies) *Service {
	factory := dependencies.IDFactory
	if factory == nil {
		factory = func(at time.Time) (string, error) {
			meta, err := domain.NewEntityMeta(at)
			return meta.ID, err
		}
	}
	return &Service{
		Host: NewHost(HostDependencies{
			Transport: dependencies.Transport, Archive: dependencies.Archive,
			Clock: dependencies.Clock,
		}),
		taskCredential: dependencies.TaskCredential, taskPath: dependencies.TaskPath,
		channelConnections: dependencies.ChannelConnections,
		idFactory:          factory,
	}
}

type systemPerformanceClock struct{}

func (systemPerformanceClock) Now() time.Time { return time.Now() }

func (service *Service) RunPerformance(ctx context.Context, command PerformanceCommand) (PerformanceReport, error) {
	return service.runPerformance(ctx, command, nil)
}

func (service *Service) RunPerformanceWithProgress(
	ctx context.Context,
	command PerformanceCommand,
	onProgress func(PerformanceProgress),
) (PerformanceReport, error) {
	return service.runPerformance(ctx, command, onProgress)
}

func (service *Service) runPerformance(
	ctx context.Context,
	command PerformanceCommand,
	onProgress func(PerformanceProgress),
) (PerformanceReport, error) {
	if service == nil {
		return PerformanceReport{}, ErrServiceUnavailable
	}
	prepared, lease, code := service.BuildPerformance(ctx, command)
	if code != "" {
		report := prepared.report()
		report.ErrorCode = code
		return report, nil
	}
	return service.Host.ExecutePrepared(ctx, prepared, lease, ExecutionOptions{OnProgress: onProgress})
}
