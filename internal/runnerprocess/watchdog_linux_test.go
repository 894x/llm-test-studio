package runnerprocess_test

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNotifyReadinessWatchdogAndDiskBudgetFailure(t *testing.T) {
	path, config := newProcessConfig(t)
	socket := filepath.Join(t.TempDir(), "s")
	connection, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: socket, Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	child := startRunner(t, path, socket)
	for _, expected := range []string{"READY=1", "WATCHDOG=1"} {
		if err := connection.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
			t.Fatal(err)
		}
		message := make([]byte, 128)
		size, _, err := connection.ReadFromUnix(message)
		if err != nil || string(message[:size]) != expected {
			t.Fatalf(
				"notification=%q, want %q: %v",
				message[:size],
				expected,
				err,
			)
		}
	}
	filler, err := os.Create(filepath.Join(path, "budget-test"))
	if err != nil {
		t.Fatal(err)
	}
	if err := filler.Truncate(int64(config.MaxStorageBytes) + 1); err != nil {
		_ = filler.Close()
		t.Fatal(err)
	}
	_ = filler.Close()
	select {
	case <-child.done:
		if child.err == nil {
			t.Fatal("exceeded storage budget did not fail the service")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("watchdog monitor did not isolate a storage budget failure")
	}
}
