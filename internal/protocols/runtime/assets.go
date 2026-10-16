package runtime

import (
	"context"
	"io"
)

type AssetMetadata struct {
	SHA256    string
	Size      int64
	MediaType string
}

// AssetStream owns one opened asset. Close must be safe concurrently with Read
// and unblock a blocked Read so cancellation can release every opened stream.
type AssetStream interface {
	io.ReadCloser
	Metadata() AssetMetadata
}

type AssetResolver interface {
	Open(context.Context, string) (AssetStream, error)
}

type PreparedFile struct {
	InputName string
	Pointer   string
	AssetRef  string
	SHA256    string
	Size      int64
	MediaType string
	Encoding  string
}

// PreparedWire describes bytes prepared by the transport, never a template or
// the redacted request view retained in a persisted Exchange.
type PreparedWire struct {
	SHA256 string
	Bytes  int64
	Files  []PreparedFile
}

func (wire PreparedWire) Clone() PreparedWire {
	wire.Files = append([]PreparedFile{}, wire.Files...)
	return wire
}
