package sqlite

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
)

func TestRelationStorageErrorPreservesContext(t *testing.T) {
	t.Parallel()

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := relationStorageError(cancelled, "relation", errors.New("query failed")); !errors.Is(err, context.Canceled) {
		t.Fatalf("relationStorageError(cancelled) = %v, want context.Canceled", err)
	}
	if err := relationStorageError(context.Background(), "relation", context.DeadlineExceeded); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("relationStorageError(deadline) = %v, want context.DeadlineExceeded", err)
	}
	if err := relationStorageError(context.Background(), "relation", errors.New("query failed")); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("relationStorageError(storage) = %v, want ErrCorrupt", err)
	}
}

func TestRepositoryDSNForcesImmediateTxLock(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name, input, base string
		pragmas           int
	}{
		{"windows path", `E:\data\llm-test.db`, `E:\data\llm-test.db`, 0},
		{"existing query", `file:E:/data/llm-test.db?mode=rwc&_pragma=foreign_keys(1)&_pragma=busy_timeout(10)&_txlock=deferred`, `file:E:/data/llm-test.db`, 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := repositoryDSN(test.input)
			if err != nil {
				t.Fatal(err)
			}
			base, rawQuery, found := strings.Cut(got, "?")
			if !found || base != test.base {
				t.Fatalf("repositoryDSN() = %q, want base %q and query", got, test.base)
			}
			query, err := url.ParseQuery(rawQuery)
			if err != nil {
				t.Fatal(err)
			}
			if got := query["_txlock"]; len(got) != 1 || got[0] != "immediate" {
				t.Fatalf("_txlock = %v, want only immediate", got)
			}
			if got := len(query["_pragma"]); got != test.pragmas {
				t.Fatalf("_pragma count = %d, want %d", got, test.pragmas)
			}
		})
	}
	if _, err := repositoryDSN("database.db?bad=%"); err == nil {
		t.Fatal("repositoryDSN() accepted malformed query escaping")
	}
}
