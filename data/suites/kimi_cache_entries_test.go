package suitebundle_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	suitebundle "github.com/894x/llm-test-studio/data/suites"
	"github.com/894x/llm-test-studio/internal/application/suitecatalog"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/persistence/workspace"
)

func TestKimiCacheHasTwoSelectableSuitesPerProtocolAndResolvesPublishedReferences(t *testing.T) {
	ctx := context.Background()
	service, err := suitecatalog.New(suitecatalog.Options{
		Builtin: suitebundle.Bundle, UserRoot: filepath.Join(t.TempDir(), "suites"),
	})
	if err != nil {
		t.Fatal(err)
	}
	repository := workspace.Catalog{Suites: service}
	suites, err := repository.ListSuites(ctx)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[domain.Protocol]int{}
	for _, suite := range suites {
		if !strings.Contains(suite.Key, ".kimi.cache") {
			continue
		}
		if suite.Key != string(suite.Protocol)+".kimi.cache" && suite.Key != string(suite.Protocol)+".kimi.cache-ttl" {
			t.Fatalf("retired cache entry is still selectable: %s", suite.Key)
		}
		counts[suite.Protocol]++
	}
	for _, protocol := range []domain.Protocol{
		domain.ProtocolOpenAIChat, domain.ProtocolOpenAIResponses, domain.ProtocolAnthropicMessages,
	} {
		if counts[protocol] != 2 {
			t.Fatalf("%s has %d cache suites, want 2", protocol, counts[protocol])
		}
	}
	for _, test := range []struct {
		id, key string
	}{
		{id: "7983a25f-615e-5576-8b6d-ec49757f204a", key: "openai-chat.kimi.cache-usage"},
		{id: "78914e5a-1ad2-51d4-931f-77ee3a413ba2", key: "openai-chat.kimi.cache-headers"},
		{id: "3cb36fb9-3e06-53c5-a019-bf713f28ce76", key: "openai-responses.kimi.cache-usage"},
		{id: "da0daa79-690b-521d-baed-da621e7fc7b1", key: "openai-responses.kimi.cache-headers"},
		{id: "7a39d5ed-bece-5daa-a7cb-dc445dc59a68", key: "anthropic-messages.kimi.cache-usage"},
		{id: "6c494a5e-3bf5-5eb2-a379-6fa55a22e08c", key: "anthropic-messages.kimi.cache-headers"},
	} {
		suite, err := repository.GetSuite(ctx, test.id)
		if err != nil || suite.ID != test.id || suite.Key != test.key || len(suite.Cases) != 10 {
			t.Fatalf("saved plan reference no longer resolves: %+v, %v", suite, err)
		}
		if _, _, err := suite.ResolveInputs(nil); err != nil {
			t.Fatalf("legacy input binding failed for %s: %v", test.key, err)
		}
	}
}
