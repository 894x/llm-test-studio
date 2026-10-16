package runnerstate

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	"golang.org/x/sys/unix"
)

func TestContentPhysicalIdentityUsesRealBirthTimeAndStableReopen(t *testing.T) {
	state := contentTestState(t)
	document := []byte("private physical object")
	identity := contentTestIdentity(document)
	if _, err := state.PutContent(t.Context(), identity, bytes.NewReader(document)); err != nil {
		t.Fatal(err)
	}
	first, err := state.OpenContent(t.Context(), identity)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	storeID := "22222222-2222-4222-8222-222222222222"
	before, err := first.PhysicalIdentity(storeID)
	if err != nil {
		t.Fatal("real statx identity unavailable", err)
	}
	if !domain.IsUUID(before.IncarnationID) || !jobcontract.IsSHA256(before.WitnessSHA256) || !jobcontract.IsSHA256(before.ChangeToken) {
		t.Fatal("invalid kernel-derived identity")
	}
	second, err := state.OpenContent(t.Context(), identity)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	again, err := second.PhysicalIdentity(storeID)
	if err != nil || before != again {
		t.Fatal("same file reopened with different identity", err)
	}
	var stat unix.Statx_t
	if err := unix.Statx(int(first.file.Fd()), "", unix.AT_EMPTY_PATH, contentStatxMask, &stat); err != nil {
		t.Fatal(err)
	}
	t.Logf("actual_statx_mask=%#x btime_sec=%d btime_nsec=%d inode=%d", stat.Mask, stat.Btime.Sec, stat.Btime.Nsec, stat.Ino)
	for _, change := range []func(*unix.Statx_t){
		func(s *unix.Statx_t) { s.Mask &^= unix.STATX_BTIME },
		func(s *unix.Statx_t) { s.Btime.Sec, s.Btime.Nsec = 0, 0 },
	} {
		missing := stat
		change(&missing)
		if _, err := contentKernelIdentity(storeID, missing, first.info); !errors.Is(err, jobcontract.ErrUnsupported) {
			t.Fatal("missing birth identity used a fallback", err)
		}
	}
	other, err := first.PhysicalIdentity("33333333-3333-4333-8333-333333333333")
	if err != nil || other.IncarnationID == before.IncarnationID {
		t.Fatal("incarnation escaped store domain", err)
	}
	touched := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(filepath.Join(state.Path(), contentName(identity)), touched, touched); err != nil {
		t.Fatal(err)
	}
	if err := first.Check(); err == nil {
		t.Fatal("touch did not invalidate the original pin")
	}
	third, err := state.OpenContent(t.Context(), identity)
	if err != nil {
		t.Fatal(err)
	}
	defer third.Close()
	after, err := third.PhysicalIdentity(storeID)
	if err != nil || before.IncarnationID != after.IncarnationID || before.WitnessSHA256 == after.WitnessSHA256 || before.ChangeToken == after.ChangeToken {
		t.Fatal("touch changed incarnation or failed to change witness/change token", err)
	}
}

func TestContentPhysicalIdentityRejectsChangedFileAndClosedState(t *testing.T) {
	for _, mode := range []string{"file_replaced", "hard_link", "symbolic_link", "root_replaced", "state_closed"} {
		t.Run(mode, func(t *testing.T) {
			state := contentTestState(t)
			document := []byte("pinned object")
			identity := contentTestIdentity(document)
			if _, err := state.PutContent(t.Context(), identity, bytes.NewReader(document)); err != nil {
				t.Fatal(err)
			}
			reader, err := state.OpenContent(t.Context(), identity)
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			path := filepath.Join(state.Path(), contentName(identity))
			switch mode {
			case "file_replaced":
				if err := os.Rename(path, path+".retained"); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, document, 0600); err != nil {
					t.Fatal(err)
				}
			case "hard_link":
				if err := os.Link(path, path+".linked"); err != nil {
					t.Fatal(err)
				}
			case "symbolic_link":
				if err := os.Rename(path, path+".retained"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(path+".retained", path); err != nil {
					t.Fatal(err)
				}
			case "root_replaced":
				if err := os.Rename(state.Path(), state.Path()+"-retained"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(state.Path(), 0700); err != nil {
					t.Fatal(err)
				}
			case "state_closed":
				if err := state.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := reader.PhysicalIdentity("22222222-2222-4222-8222-222222222222"); err == nil {
				t.Fatal("changed or closed physical object accepted")
			}
		})
	}
}
