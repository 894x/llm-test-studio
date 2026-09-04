// Package suitebundle exposes the immutable suite catalog shipped with llm-test-studio.
package suitebundle

import "embed"

// Bundle contains every built-in suite definition. The files are read directly
// by the catalog and are never seeded into SQLite.
//
//go:embed */*/suite.json
var Bundle embed.FS
