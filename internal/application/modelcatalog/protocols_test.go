package modelcatalog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
)

func TestModelProtocolArrayPersistsAndIsCopied(t *testing.T) {
	service, err := New(filepath.Join(t.TempDir(), "models.json"))
	if err != nil {
		t.Fatal(err)
	}
	model := validModel("40000000-0000-4000-8000-000000000001", time.Now().UTC())
	model.Protocols = []domain.Protocol{domain.ProtocolOpenAIChat, domain.ProtocolOpenAIResponses}
	if err := service.Create(context.Background(), model); err != nil {
		t.Fatal(err)
	}
	got, err := service.Get(context.Background(), model.ID)
	if err != nil || len(got.Protocols) != 2 {
		t.Fatalf("model = %v, error = %v", got, err)
	}
	got.Protocols[0] = domain.ProtocolSeedance
	again, err := service.Get(context.Background(), model.ID)
	if err != nil || again.Protocols[0] != domain.ProtocolOpenAIChat {
		t.Fatal("returned protocols changed persisted model")
	}
}

func TestOldModelProtocolFieldFailsWithoutChangingTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models.json")
	model := validModel("40000000-0000-4000-8000-000000000001", time.Now().UTC())
	raw, err := json.Marshal([]domain.Model{model})
	if err != nil {
		t.Fatal(err)
	}
	raw = bytes.Replace(raw, []byte(`"protocols":["openai-chat"]`), []byte(`"protocol":"openai-chat"`), 1)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	service, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.List(context.Background())
	if !errors.Is(err, ErrCorrupt) || !strings.Contains(err.Error(), "protocols arrays") {
		t.Fatalf("old model error = %v", err)
	}
	if err := service.Create(context.Background(), model); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("write over old catalog = %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(after, raw) {
		t.Fatal("rejected old catalog changed")
	}
}
