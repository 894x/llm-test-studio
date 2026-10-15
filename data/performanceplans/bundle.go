// Package performanceplans exposes the built-in quick performance plans.
package performanceplans

import "embed"

// Bundle contains current-format authored plans, read without importing user data.
//
//go:embed *.json
var Bundle embed.FS
