// Package casebundle exposes the immutable case catalog shipped with llm-test-studio.
package casebundle

import "embed"

// Bundle contains every built-in Case definition and the auxiliary load
// template. Catalog readers discover definitions by layout, without a group list.
//
//go:embed */*/case.json kimi-k3/load-profile-32k.json
var Bundle embed.FS
