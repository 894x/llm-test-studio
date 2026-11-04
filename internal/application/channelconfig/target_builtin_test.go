package channelconfig

import (
	"errors"
	"testing"

	"github.com/894x/llm-test-studio/internal/application/catalog"
	"github.com/894x/llm-test-studio/internal/domain"
)

func TestTargetModelSelectionKeepsExistingUserIdentity(t *testing.T) {
	user := domain.Model{EntityMeta: domain.EntityMeta{ID: "user-id"}, Name: "same-name"}
	builtin := domain.Model{EntityMeta: domain.EntityMeta{ID: "builtin-id"}, Name: user.Name, Builtin: true}
	secondBuiltin := builtin
	secondBuiltin.ID = "other-builtin-id"
	for _, models := range [][]domain.Model{{user, builtin}, {builtin, user}, {builtin, secondBuiltin, user}} {
		got, err := selectTargetModel(models, user.Name)
		if err != nil || got.ID != user.ID || got.Builtin {
			t.Fatalf("user model was not preserved: %#v, %v", got, err)
		}
	}
	duplicate := user
	duplicate.ID = "other-user-id"
	if _, err := selectTargetModel([]domain.Model{builtin, user, duplicate}, user.Name); !errors.Is(err, catalog.ErrConflict) {
		t.Fatalf("two user models with the same name were accepted: %v", err)
	}
}
