package domain

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"testing"
)

func TestValidationMemoizationDoesNotTrustEditedDefinitions(t *testing.T) {
	definitions := validProtocolDefinitions()
	if err := definitions.Validate(); err != nil {
		t.Fatal(err)
	}
	definitions[ProtocolOpenAIChat] = json.RawMessage(`{"api_key":"plaintext"}`)
	if _, err := json.Marshal(definitions); err == nil {
		t.Fatal("cached validity admitted edited secret-bearing body")
	}
}

func TestDefinitionValidationCacheEvictsOldKeys(t *testing.T) {
	cache := definitionValidationCache{keys: map[definitionValidationKey]struct{}{}, order: []definitionValidationKey{}}
	for index := range definitionValidationCacheLimit + 1 {
		cache.remember(definitionValidationKey{protocol: ProtocolOpenAIChat, digest: sha256.Sum256([]byte(fmt.Sprint(index)))})
	}
	oldest := definitionValidationKey{protocol: ProtocolOpenAIChat, digest: sha256.Sum256([]byte("0"))}
	if len(cache.keys) != definitionValidationCacheLimit || cache.contains(oldest) {
		t.Fatal("validation cache did not stay bounded")
	}
}
