package catalog

import "context"

// Counts omits definitions and credential references from the overview payload.
type Counts struct {
	Models             int `json:"models"`
	Channels           int `json:"channels"`
	EnabledChannels    int `json:"enabled_channels"`
	ConfiguredChannels int `json:"configured_channels"`
	ChannelModels      int `json:"channel_models"`
	TestCases          int `json:"test_cases"`
	Suites             int `json:"suites"`
	Plans              int `json:"plans"`
}

func (service *Service) Counts(ctx context.Context) (Counts, error) {
	counts := Counts{}
	ctx, err := service.ready(ctx)
	if err != nil {
		return counts, err
	}
	models, err := service.repository.ListModels(ctx)
	if err != nil {
		return counts, service.portError(ctx, err)
	}
	counts.Models = len(models)
	channels, err := service.repository.ListChannels(ctx)
	if err != nil {
		return counts, service.portError(ctx, err)
	}
	counts.Channels = len(channels)
	for _, channel := range channels {
		if channel.Enabled {
			counts.EnabledChannels++
		}
		if channel.CredentialID != "" {
			counts.ConfiguredChannels++
		}
	}
	mappings, err := service.repository.ListChannelModels(ctx)
	if err != nil {
		return counts, service.portError(ctx, err)
	}
	counts.ChannelModels = len(mappings)
	if repository, ok := service.repository.(interface {
		CountAuthored(context.Context) (int, int, int, error)
	}); ok {
		counts.TestCases, counts.Suites, counts.Plans, err = repository.CountAuthored(ctx)
		if err != nil {
			return counts, service.portError(ctx, err)
		}
		return counts, nil
	}
	cases, err := service.repository.ListTestCases(ctx)
	if err != nil {
		return counts, service.portError(ctx, err)
	}
	counts.TestCases = len(cases)
	suites, err := service.repository.ListSuites(ctx)
	if err != nil {
		return counts, service.portError(ctx, err)
	}
	counts.Suites = len(suites)
	plans, err := service.repository.ListPlans(ctx)
	if err != nil {
		return counts, service.portError(ctx, err)
	}
	counts.Plans = len(plans)
	return counts, nil
}
