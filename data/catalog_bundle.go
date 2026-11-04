// Package builtinbundle contains the file-authored model and plan defaults.
package builtinbundle

import "embed"

//go:embed definitions/models/*.json plans/*.json
var Bundle embed.FS
