package ssh

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pkg/sftp"
	cryptossh "golang.org/x/crypto/ssh"
)

const activityTestTimeout = 80 * time.Millisecond

type countedRequests struct {
	cryptossh.Conn
	calls  atomic.Int64
	active atomic.Int64
	peak   atomic.Int64
	first  chan struct{}
	start  sync.Once
}

func (connection *countedRequests) SendRequest(name string, reply bool, payload []byte) (bool, []byte, error) {
	connection.calls.Add(1)
	active := connection.active.Add(1)
	connection.start.Do(func() { close(connection.first) })
	defer connection.active.Add(-1)
	for {
		previous := connection.peak.Load()
		if active <= previous || connection.peak.CompareAndSwap(previous, active) {
			break
		}
	}
	return connection.Conn.SendRequest(name, reply, payload)
}

func startActivityKeepalive(client *Client) *countedRequests {
	close(client.stop)
	<-client.done
	counted := &countedRequests{Conn: client.connection.Conn, first: make(chan struct{})}
	client.connection.Conn = counted
	client.stop, client.done = make(chan struct{}), make(chan struct{})
	go client.keepAlive(20*time.Millisecond, activityTestTimeout)
	return counted
}

type activitySFTPFiles struct{ reader *bytes.Reader }

func (files activitySFTPFiles) Fileread(*sftp.Request) (io.ReaderAt, error) { return files, nil }

func (files activitySFTPFiles) ReadAt(destination []byte, offset int64) (int, error) {
	time.Sleep(10 * time.Millisecond)
	return files.reader.ReadAt(destination, offset)
}

type activityDestination struct {
	first func()
	bytes atomic.Int64
}

func (destination *activityDestination) Write(document []byte) (int, error) {
	destination.bytes.Add(int64(len(document)))
	destination.first()
	return len(document), nil
}

type activityFixture struct {
	client   *Client
	requests *countedRequests
	first    chan struct{}
	release  func()
	bytes    *activityDestination
	size     int64
}

func newActivityFixture(t *testing.T) *activityFixture {
	t.Helper()
	peer := newTestPeer(t)
	ack := make(chan struct{})
	release := sync.OnceFunc(func() { close(ack) })
	t.Cleanup(release)
	peer.keepaliveReply = func(request *cryptossh.Request) {
		select {
		case <-ack:
			_ = request.Reply(false, nil)
		case <-peer.stop:
		}
	}
	document := bytes.Repeat([]byte("non-secret SFTP activity fixture"), 1<<18)
	files := activitySFTPFiles{reader: bytes.NewReader(document)}
	peer.sftpSession = func(channel cryptossh.Channel) {
		server := sftp.NewRequestServer(channel, sftp.Handlers{FileGet: files})
		defer server.Close()
		_ = server.Serve()
	}
	client := dialPeer(t, peer)
	first := make(chan struct{})
	return &activityFixture{
		client: client, requests: startActivityKeepalive(client), first: first, release: release,
		bytes: &activityDestination{first: sync.OnceFunc(func() { close(first) })}, size: int64(len(document)),
	}
}

func (fixture *activityFixture) read(ctx context.Context) <-chan error {
	done := make(chan error, 1)
	go func() {
		done <- fixture.client.withSFTP(ctx, sftpArtifactTimeout, func(remote *sftp.Client) error {
			file, err := remote.Open("/program")
			if err != nil {
				return err
			}
			defer file.Close()
			_, err = io.CopyBuffer(
				io.MultiWriter(sha256.New(), fixture.bytes),
				io.LimitReader(file, fixture.size+1),
				make([]byte, transferBufferBytes),
			)
			return err
		})
	}()
	return done
}

func waitActivity(t *testing.T, ready <-chan struct{}) {
	t.Helper()
	select {
	case <-ready:
	case <-time.After(2 * time.Second):
		t.Fatal("SSH activity fixture did not progress")
	}
}

func TestKeepaliveKeepsOnePendingRequestDuringSFTPActivityAndAcceptsDelayedACK(t *testing.T) {
	fixture := newActivityFixture(t)
	done := fixture.read(t.Context())
	waitActivity(t, fixture.first)
	waitActivity(t, fixture.requests.first)
	select {
	case <-fixture.client.done:
		t.Fatal("keepalive closed SSH despite continuous inbound SFTP data")
	case <-time.After(3 * activityTestTimeout):
	}
	if fixture.requests.calls.Load() != 1 || fixture.requests.active.Load() != 1 || fixture.requests.peak.Load() != 1 {
		t.Fatal("waiting for a late ACK opened another global request")
	}
	fixture.release()
	if err := <-done; err != nil || fixture.bytes.bytes.Load() != fixture.size {
		t.Fatal("delayed keepalive ACK interrupted the verified SFTP stream")
	}
	if _, err := fixture.client.Probe(t.Context()); err != nil {
		t.Fatal("late failure reply did not preserve the SSH connection")
	}
}

func TestKeepaliveClosesAfterSFTPActivityBecomesSilent(t *testing.T) {
	fixture := newActivityFixture(t)
	if err := <-fixture.read(t.Context()); err != nil || fixture.bytes.bytes.Load() != fixture.size {
		t.Fatal("keepalive interrupted the active stream before its silent window")
	}
	fixture.client.inbound.mu.Lock()
	lastRead := fixture.client.inbound.lastRead
	fixture.client.inbound.mu.Unlock()
	waitActivity(t, fixture.client.done)
	if time.Since(lastRead) < activityTestTimeout || fixture.requests.active.Load() != 0 {
		t.Fatal("silent window closed early or left the request running")
	}
}

func TestKeepaliveCloseAndCallerCancellationJoinActiveSFTPAndPendingRequest(t *testing.T) {
	for _, action := range []string{"close", "cancel"} {
		t.Run(action, func(t *testing.T) {
			fixture := newActivityFixture(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := fixture.read(ctx)
			waitActivity(t, fixture.first)
			waitActivity(t, fixture.requests.first)
			if action == "close" {
				if err := fixture.client.Close(); err != nil {
					t.Fatal("Close failed during an active transfer")
				}
			} else {
				cancel()
			}
			waitActivity(t, fixture.client.done)
			if err := <-done; err == nil || (action == "cancel" && !errors.Is(err, context.Canceled)) {
				t.Fatal("closing or canceling an active transfer was not observed")
			}
			if fixture.requests.active.Load() != 0 {
				t.Fatal("keepalive left a global request goroutine running")
			}
		})
	}
}

func TestKeepaliveClosesSilentAndOutboundOnlyConnections(t *testing.T) {
	for _, outbound := range []bool{false, true} {
		t.Run(map[bool]string{false: "silent", true: "outbound_only"}[outbound], func(t *testing.T) {
			peer := newTestPeer(t)
			peer.stallKeepalive.Store(true)
			peer.execute = func(_ string, channel cryptossh.Channel) { _, _ = io.Copy(io.Discard, channel) }
			client := dialPeer(t, peer)
			var sent atomic.Int64
			if outbound {
				session, err := client.connection.NewSession()
				if err != nil {
					t.Fatal(err)
				}
				input, err := session.StdinPipe()
				if err != nil || session.Start("outbound-only-fixture") != nil {
					t.Fatal("could not start outbound-only fixture")
				}
				defer session.Close()
				written := make(chan struct{})
				defer func() { _ = client.Close(); waitActivity(t, written) }()
				go func() {
					defer close(written)
					for {
						if _, err := input.Write([]byte("x")); err != nil {
							return
						}
						sent.Add(1)
						time.Sleep(10 * time.Millisecond)
					}
				}()
			}
			requests := startActivityKeepalive(client)
			waitActivity(t, client.done)
			if requests.active.Load() != 0 || (outbound && sent.Load() < 2) {
				t.Fatal("silent connection was retained or pending request leaked")
			}
		})
	}
}
