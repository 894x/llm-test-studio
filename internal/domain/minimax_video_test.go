package domain

import "testing"

func TestMiniMaxVideoIsAValidCatalogProtocol(t *testing.T) {
	if err := Protocol("minimax-video").Validate(); err != nil {
		t.Fatalf("minimax-video protocol rejected: %v", err)
	}
}
