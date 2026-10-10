package catalog

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
)

func TestModelAndMappingProtocolSetsCanBeEdited(t *testing.T) {
	repository := validRepository()
	service := newTestService(t, repository, fixtureTime().Add(time.Hour))
	ctx := context.Background()
	protocols := []domain.Protocol{domain.ProtocolOpenAIChat, domain.ProtocolOpenAIResponses}
	_, err := service.UpdateModel(ctx, UpdateModelCommand{
		ID: modelAID, ExpectedRevision: 1, Name: "Multi-protocol", Protocols: protocols,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(repository.updatedModel.Protocols, protocols) {
		t.Fatalf("model protocols = %v", repository.updatedModel.Protocols)
	}
	repository.models[0] = repository.updatedModel
	protocols[0] = domain.ProtocolSeedance
	if repository.updatedModel.Protocols[0] != domain.ProtocolOpenAIChat {
		t.Fatal("model shares command protocol storage")
	}

	_, err = service.UpdateChannelModel(ctx, UpdateChannelModelCommand{
		ID: mappingAID, ExpectedRevision: 1, UpstreamModelName: "responses-model",
		Protocols: []domain.Protocol{domain.ProtocolOpenAIResponses},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !repository.updatedMapping.SupportsProtocol(domain.ProtocolOpenAIResponses) {
		t.Fatal("mapping did not save the selected protocol")
	}
	for index, mapping := range repository.mappings {
		if mapping.ID == mappingAID {
			repository.mappings[index] = repository.updatedMapping
		}
	}
	_, err = service.UpdateModel(ctx, UpdateModelCommand{
		ID: modelAID, ExpectedRevision: 2, Name: "Multi-protocol",
		Protocols: []domain.Protocol{domain.ProtocolOpenAIChat},
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("removing a mapped protocol = %v", err)
	}
}

func TestMappingRejectsProtocolsOutsideItsModelWithoutWriting(t *testing.T) {
	for _, protocols := range [][]domain.Protocol{
		{}, {domain.ProtocolOpenAIChat, domain.ProtocolOpenAIChat},
		{domain.ProtocolOpenAIResponses}, {domain.Protocol("unknown")},
	} {
		repository := validRepository()
		service := newTestService(t, repository, fixtureTime().Add(time.Hour))
		_, err := service.UpdateChannelModel(context.Background(), UpdateChannelModelCommand{
			ID: mappingAID, ExpectedRevision: 1, UpstreamModelName: "model", Protocols: protocols,
		})
		if !errors.Is(err, ErrInvalid) || repository.updatedMapping.ID != "" {
			t.Fatalf("protocols %v: error=%v, written=%v", protocols, err, repository.updatedMapping.ID)
		}
	}
}
