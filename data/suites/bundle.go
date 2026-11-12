// Package suitebundle exposes the immutable suite catalog shipped with llm-test-studio.
package suitebundle

import "embed"

// Bundle contains selectable suites and retired schema-1 documents for resolving
// saved plan references. Compatibility documents are excluded from catalog lists.
//
//go:embed */*/suite.json compatibility/*/*/*.json
var Bundle embed.FS
