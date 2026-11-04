// Package casebundle exposes the immutable case catalog shipped with llm-test-studio.
package casebundle

import "embed"

// Bundle contains every built-in Case definition and the auxiliary load
// template. Catalog readers discover definitions by layout, without a group list.
//
//go:embed */*/*.json */*.json
var Bundle embed.FS
