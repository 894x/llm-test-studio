package ssh

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/894x/llm-test-studio/internal/credentials"
)

// Live validation is explicitly enabled and receives credentials only through
// stdin. The ordinary test suite never reads developer credentials or connects
// to a physical node. This test does not install or start a runner.
func TestLiveSSHProbeAndVerifiedSFTPTransfer(t *testing.T) {
	if os.Getenv("LLM_TEST_LIVE_SSH") != "1" {
		t.Skip("explicit physical-node validation only")
	}
	var input struct {
		Endpoint Endpoint `json:"endpoint"`
		Password []byte   `json:"password"`
	}
	decoder := json.NewDecoder(io.LimitReader(os.Stdin, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		t.Fatal("invalid live validation input")
	}
	lease, err := credentials.NewTemporaryLease(input.Password)
	clear(input.Password)
	if err != nil {
		t.Fatal("missing live authentication")
	}
	defer lease.Close()
	client, err := Dial(t.Context(), input.Endpoint, PasswordAuthentication(lease))
	if err != nil {
		t.Fatalf("pinned connection failed: %v", err)
	}
	defer client.Close()
	_ = lease.Close()
	info, err := client.Probe(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	document, err := client.runAdministrativeScript(
		t.Context(),
		"set -eu\numask 077\nmktemp -d /tmp/llm-test-ssh-validation.XXXXXXXXXX\n",
	)
	if err != nil {
		t.Fatal(err)
	}
	directory := strings.TrimSpace(string(document))
	valid := strings.HasPrefix(directory, "/tmp/llm-test-ssh-validation.") && safeRemotePath(directory)
	for _, character := range strings.TrimPrefix(directory, "/tmp/llm-test-ssh-validation.") {
		letter := character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z'
		digit := character >= '0' && character <= '9'
		valid = valid && (letter || digit)
	}
	if !valid {
		t.Fatal("node returned an invalid validation directory")
	}
	defer func() {
		// Only these exact, validated non-secret test paths are removed.
		_, err := client.runAdministrativeScript(t.Context(), "set -eu\nrm -f -- '"+directory+"/program'\nrmdir -- '"+directory+"'\n")
		if err != nil {
			t.Errorf("validation artifact cleanup failed at %s", directory)
		}
	}()
	fixture := bytes.Repeat([]byte("verified non-secret program transfer fixture\n"), 4096)
	digest := sha256.Sum256(fixture)
	checksum := hex.EncodeToString(digest[:])
	upload := Upload{
		Path: directory + "/program", Size: int64(len(fixture)), SHA256: checksum, Mode: 0700,
		Source: bytes.NewReader(fixture),
	}
	wrong := upload
	wrong.SHA256, wrong.Source = strings.Repeat("a", 64), bytes.NewReader(fixture)
	if err := client.UploadNew(t.Context(), wrong); !errors.Is(err, ErrIntegrity) {
		t.Fatal("node accepted an upload with the wrong digest")
	}
	if err := client.UploadNew(t.Context(), upload); err != nil {
		t.Fatalf("verified upload failed: %v", err)
	}
	upload.Source = bytes.NewReader(fixture)
	if err := client.UploadNew(t.Context(), upload); !errors.Is(err, ErrExists) {
		t.Fatal("upload could overwrite an existing file")
	}
	readback, err := client.ReadPrivate(t.Context(), upload.Path, info.UID)
	if err != nil || !bytes.Equal(readback, fixture) {
		t.Fatal("private upload readback did not preserve the original bytes")
	}
	clear(readback)
	result, _ := json.Marshal(struct {
		System SystemInfo `json:"system"`
		Size   int        `json:"transfer_bytes"`
		SHA256 string     `json:"sha256"`
	}{System: info, Size: len(fixture), SHA256: checksum})
	t.Log(string(result))
}
