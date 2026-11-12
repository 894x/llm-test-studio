package suitecatalog

import (
	"context"
	"fmt"
	"io/fs"
	"strings"
)

// Retired built-in suites remain resolvable by their published IDs for saved
// plans. Their schema-1 documents are outside the selectable group/suite layout.
func (service *Service) findCompatibility(ctx context.Context, id string) (Entry, error) {
	if err := ctx.Err(); err != nil {
		return Entry{}, err
	}
	paths, err := fs.Glob(service.builtin, "compatibility/"+id+"/*/*.json")
	if err != nil {
		return Entry{}, err
	}
	if len(paths) == 0 {
		return Entry{}, fs.ErrNotExist
	}
	if len(paths) != 1 {
		return Entry{}, fmt.Errorf("%w: compatibility suite %s", ErrCollision, id)
	}
	path := paths[0]
	parts := strings.Split(path, "/")
	group, directory := parts[2], strings.TrimSuffix(parts[3], ".json")
	if !validSegment(group) || !validSegment(directory) {
		return Entry{}, ErrInvalid
	}
	raw, err := fs.ReadFile(service.builtin, path)
	if err != nil {
		return Entry{}, err
	}
	suite, err := decodeDocument(raw)
	if err != nil || suite.ID != id || string(suite.Protocol) != group {
		return Entry{}, fmt.Errorf("%w: compatibility suite %q", ErrInvalid, path)
	}
	return Entry{Group: group, Directory: directory, Source: SourceBuiltin, Suite: suite}, nil
}
