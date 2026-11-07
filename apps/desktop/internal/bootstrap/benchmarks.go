package bootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"

	"github.com/894x/llm-test-studio/internal/application/benchmarks"
	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/benchmarkruntime"
	"github.com/894x/llm-test-studio/internal/credentials"
	workspacestore "github.com/894x/llm-test-studio/internal/persistence/workspace"
)

func newBenchmarkService(
	ctx context.Context,
	directory string,
	repository workspacestore.Catalog,
	credentialStore credentials.Store,
) (*benchmarks.Service, error) {
	runtime, err := benchmarkruntime.New(filepath.Join(directory, "benchmark-runtime"))
	if err != nil {
		return nil, err
	}
	resolver := quicktest.NewStoredChannelConnectionResolver(repository, credentialStore)
	cacheRoot, err := os.UserCacheDir()
	if err != nil {
		return nil, err
	}
	workspaceHash := sha256.Sum256([]byte(filepath.Clean(directory)))
	// Keep data caches separate from durable reports and their paths short on Windows.
	cacheDirectory := filepath.Join(cacheRoot, "llm-test", "d", hex.EncodeToString(workspaceHash[:8]))
	return benchmarks.New(ctx, benchmarks.Dependencies{Directory: filepath.Join(directory, "benchmarks"),
		CacheDirectory: cacheDirectory,
		CatalogJSON:    benchmarkruntime.CatalogJSON(), Runtime: runtime,
		Resolve: func(ctx context.Context, input benchmarks.Connection) (benchmarks.ResolvedConnection, error) {
			if input.ChannelID == "" {
				return benchmarks.DirectConnection(ctx, input)
			}
			selection := quicktest.ChannelSelection{ChannelID: input.ChannelID, Model: input.Model, Protocol: input.Protocol}
			resolved, err := resolver.Resolve(ctx, selection)
			if err != nil {
				return benchmarks.ResolvedConnection{}, err
			}
			channel, err := repository.GetChannel(ctx, input.ChannelID)
			if err != nil {
				clear(resolved.APIKey)
				return benchmarks.ResolvedConnection{}, err
			}
			return benchmarks.ResolvedConnection{Target: benchmarks.Target{Protocol: input.Protocol, Model: input.Model,
				ChannelID: input.ChannelID, ChannelName: channel.Name, BaseURL: resolved.BaseURL}, APIKey: resolved.APIKey}, nil
		}})
}
