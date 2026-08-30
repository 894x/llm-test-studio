// Package casebundle exposes the immutable case catalog shipped with llm-studio.
package casebundle

import "embed"

// Bundle contains every versioned legacy case source plus load templates that
// require an explicit future conversion. Importers must treat the paths and
// bytes as immutable source material and persist provenance separately.
//
//go:embed openai-chat/*/case.json kimi-k3/*/case.json seedance/*/case.json kimi-k3/load-profile-32k.json
var Bundle embed.FS
