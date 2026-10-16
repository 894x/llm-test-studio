package protocols

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/protocol"
	"github.com/894x/llm-test-studio/internal/protocols/runtime"
	"github.com/894x/llm-test-studio/internal/testspec"
)

const (
	maxFileEncoders     = 4
	fileEncoderCharge   = 16 << 20
	maxFilePayloadBytes = maxFileEncoders * fileEncoderCharge
	maxImageDimension   = 4096
	maxImagePixels      = 16_777_216
)

// This is a process-wide limit on additional file payload ownership, rather
// than a claim about total Go heap or RSS.
var sharedFileEncoders = make(chan struct{}, maxFileEncoders)

var errWireLimit = errors.New("protocol request exceeds the 4 MiB wire limit")

type wireBody struct {
	ctx       context.Context
	mu        sync.Mutex
	data      []byte
	position  int
	closed    bool
	closeDone chan struct{}
	closeErr  error
	asset     *ownedAsset
	image     []byte
	release   func()
	view      json.RawMessage
	metadata  *runtime.PreparedWire
}

func (body *wireBody) Read(destination []byte) (int, error) {
	body.mu.Lock()
	defer body.mu.Unlock()
	if body.closed {
		return 0, io.ErrClosedPipe
	}
	if err := body.ctx.Err(); err != nil {
		return 0, err
	}
	if len(destination) == 0 {
		return 0, nil
	}
	if body.position == len(body.data) {
		return 0, io.EOF
	}
	count := copy(destination, body.data[body.position:])
	body.position += count
	return count, nil
}

func (body *wireBody) Close() error {
	body.mu.Lock()
	if body.closed {
		body.mu.Unlock()
		<-body.closeDone
		return body.closeErr
	}
	body.closed = true
	// Read never exposes data and holds the same mutex while copying. After
	// this lock all prior body reads have finished and later reads fail.
	clear(body.data)
	body.data = nil
	clear(body.image)
	body.image = nil
	body.mu.Unlock()
	if body.asset != nil {
		body.closeErr = body.asset.Close()
	}
	if body.release != nil {
		body.release()
	}
	close(body.closeDone)
	return body.closeErr
}

type ownedAsset struct {
	stream runtime.AssetStream
	once   sync.Once
	done   chan struct{}
	err    error
}

func (asset *ownedAsset) Close() error {
	asset.once.Do(func() {
		asset.err = asset.stream.Close()
		close(asset.done)
	})
	<-asset.done
	return asset.err
}

func prepareWire(
	ctx context.Context,
	request runtime.Request,
	channel domain.ChannelSnapshot,
) (_ *wireBody, err error) {
	body := &wireBody{ctx: ctx, closeDone: make(chan struct{}), view: json.RawMessage{}}
	complete := false
	defer func() {
		if !complete {
			_ = body.Close()
		}
	}()
	if len(request.Body.Body) > testspec.MaxDocumentBytes || len(request.Body.Files) > 1 {
		return nil, ErrInvalidExecution
	}
	if len(request.Body.Body) == 0 {
		if len(request.Body.Files) != 0 {
			return nil, ErrInvalidExecution
		}
		complete = true
		return body, nil
	}
	if len(request.Body.Files) > 0 {
		validRoute := channel.Protocol == domain.ProtocolOpenAIChat &&
			request.Method == http.MethodPost && request.Path == protocol.OpenAIChatPath
		if !validRoute || request.Assets == nil {
			return nil, testspec.ErrFileInputsUnsupported
		}
		select {
		case sharedFileEncoders <- struct{}{}:
			var once sync.Once
			body.release = func() { once.Do(func() { <-sharedFileEncoders }) }
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if err := request.Body.Validate(); err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(request.Body.Body))
	decoder.UseNumber()
	var root map[string]any
	if err := decoder.Decode(&root); err != nil || root == nil {
		return nil, ErrInvalidExecution
	}
	model, err := json.Marshal(channel.UpstreamModelName)
	if err != nil {
		return nil, ErrInvalidExecution
	}
	root["model"] = json.RawMessage(model)
	files := []runtime.PreparedFile{}
	var occurrence *testspec.FileOccurrence
	if len(request.Body.Files) == 1 {
		file := request.Body.Files[0]
		occurrence = &file
		stream, openErr := request.Assets.Open(ctx, file.AssetRef)
		if openErr != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, ErrInvalidExecution
		}
		if stream == nil {
			return nil, ErrInvalidExecution
		}
		body.asset = &ownedAsset{stream: stream, done: make(chan struct{})}
		stop := context.AfterFunc(ctx, func() { _ = body.asset.Close() })
		defer stop()
		metadata := stream.Metadata()
		if !validAssetMetadata(metadata, file.File) {
			return nil, ErrInvalidExecution
		}
		body.image, err = io.ReadAll(io.LimitReader(stream, file.File.MaxBytes+1))
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err != nil || int64(len(body.image)) != metadata.Size {
			return nil, ErrInvalidExecution
		}
		digest := sha256.Sum256(body.image)
		if hex.EncodeToString(digest[:]) != metadata.SHA256 || !validImage(body.image, metadata.MediaType) {
			return nil, ErrInvalidExecution
		}
		files = append(files, runtime.PreparedFile{
			InputName: file.InputName, Pointer: file.Pointer, AssetRef: file.AssetRef,
			SHA256: metadata.SHA256, Size: metadata.Size, MediaType: metadata.MediaType,
			Encoding: file.File.Encoding,
		})
	}
	output := boundedWire{data: []byte{}, limit: testspec.MaxDocumentBytes}
	encoding := wireEncoding{file: occurrence, image: body.image, files: files}
	if err := encoding.write(&output, root, ""); err != nil {
		clear(output.data)
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		clear(output.data)
		return nil, err
	}
	body.data = output.data
	sum := sha256.Sum256(body.data)
	body.metadata = &runtime.PreparedWire{
		SHA256: hex.EncodeToString(sum[:]), Bytes: int64(len(body.data)), Files: files,
	}
	if occurrence == nil {
		body.view = bytes.Clone(body.data)
	} else {
		view := boundedWire{data: []byte{}, limit: maxEvidenceValueBytes}
		encoding.image, encoding.view = nil, true
		if err := encoding.write(&view, root, ""); err != nil {
			clear(view.data)
			if !errors.Is(err, errWireLimit) {
				return nil, err
			}
			view.data = []byte(`{"_evidence":"omitted_size_limit"}`)
		}
		body.view = view.data
	}
	complete = true
	return body, nil
}

func validAssetMetadata(metadata runtime.AssetMetadata, file testspec.FileInput) bool {
	digest, err := hex.DecodeString(metadata.SHA256)
	validDigest := err == nil && len(digest) == sha256.Size &&
		strings.ToLower(metadata.SHA256) == metadata.SHA256
	validSize := metadata.Size > 0 && metadata.Size <= file.MaxBytes &&
		metadata.Size <= testspec.MaxFileInputBytes
	return validDigest && validSize && slices.Contains(file.MediaTypes, metadata.MediaType)
}

func validImage(data []byte, mediaType string) bool {
	var width, height int
	switch mediaType {
	case "image/png":
		config, err := png.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			return false
		}
		width, height = config.Width, config.Height
	case "image/jpeg":
		config, err := jpeg.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			return false
		}
		width, height = config.Width, config.Height
	default:
		return false
	}
	validDimensions := width > 0 && height > 0 && width <= maxImageDimension && height <= maxImageDimension
	return validDimensions && int64(width)*int64(height) <= maxImagePixels
}

type boundedWire struct {
	data  []byte
	limit int
}

func (wire *boundedWire) Write(value []byte) (int, error) {
	if len(value) > wire.limit-len(wire.data) {
		return 0, errWireLimit
	}
	wire.data = append(wire.data, value...)
	return len(value), nil
}

type wireEncoding struct {
	file  *testspec.FileOccurrence
	image []byte
	files []runtime.PreparedFile
	view  bool
}

func (encoding wireEncoding) write(output *boundedWire, value any, pointer string) error {
	if encoding.file != nil && pointer == encoding.file.Pointer {
		return encoding.writeFile(output)
	}
	switch current := value.(type) {
	case map[string]any:
		if _, err := output.Write([]byte("{")); err != nil {
			return err
		}
		names := make([]string, 0, len(current))
		for name := range current {
			names = append(names, name)
		}
		sort.Strings(names)
		for index, name := range names {
			if index > 0 {
				if _, err := output.Write([]byte(",")); err != nil {
					return err
				}
			}
			key, _ := json.Marshal(name)
			if _, err := output.Write(append(key, ':')); err != nil {
				return err
			}
			escaped := strings.ReplaceAll(strings.ReplaceAll(name, "~", "~0"), "/", "~1")
			if err := encoding.write(output, current[name], pointer+"/"+escaped); err != nil {
				return err
			}
		}
		_, err := output.Write([]byte("}"))
		return err
	case []any:
		if _, err := output.Write([]byte("[")); err != nil {
			return err
		}
		for index, item := range current {
			if index > 0 {
				if _, err := output.Write([]byte(",")); err != nil {
					return err
				}
			}
			if err := encoding.write(output, item, pointer+"/"+strconv.Itoa(index)); err != nil {
				return err
			}
		}
		_, err := output.Write([]byte("]"))
		return err
	default:
		raw, err := json.Marshal(value)
		if err != nil {
			return ErrInvalidExecution
		}
		_, err = output.Write(raw)
		return err
	}
}

func (encoding wireEncoding) writeFile(output *boundedWire) error {
	if encoding.view {
		raw, err := json.Marshal(map[string]any{
			"_evidence": "redacted_asset", "asset_ref": encoding.file.AssetRef,
			"sha256": encoding.files[0].SHA256, "size": encoding.files[0].Size,
			"media_type": encoding.files[0].MediaType,
		})
		if err != nil {
			return ErrInvalidExecution
		}
		_, err = output.Write(raw)
		return err
	}
	prefix := `"data:` + encoding.files[0].MediaType + ";base64,"
	if _, err := output.Write([]byte(prefix)); err != nil {
		return err
	}
	encoder := base64.NewEncoder(base64.StdEncoding, output)
	if _, err := encoder.Write(encoding.image); err != nil {
		return err
	}
	if err := encoder.Close(); err != nil {
		return err
	}
	_, err := output.Write([]byte(`"`))
	return err
}
