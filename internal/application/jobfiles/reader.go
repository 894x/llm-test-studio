package jobfiles

import (
	"context"
	"io"
)

type selectionReader struct {
	registry *Registry
	entry    *selectionEntry
	ctx      context.Context
	position int
	closed   bool
}

func (reader *selectionReader) Read(destination []byte) (int, error) {
	reader.registry.mu.Lock()
	defer reader.registry.mu.Unlock()
	if reader.closed {
		return 0, ErrClosed
	}
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	if len(destination) == 0 {
		return 0, nil
	}
	if reader.position == len(reader.entry.data) {
		return 0, io.EOF
	}
	count := copy(destination, reader.entry.data[reader.position:])
	reader.position += count
	return count, nil
}

func (reader *selectionReader) Close() error {
	reader.registry.mu.Lock()
	defer reader.registry.mu.Unlock()
	if reader.closed {
		return nil
	}
	reader.closed = true
	delete(reader.entry.readers, reader)
	reader.registry.discardLocked(reader.entry)
	return nil
}
