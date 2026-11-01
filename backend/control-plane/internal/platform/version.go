// Package platform holds build-level metadata shared across the control plane.
package platform

import "runtime"

// Build information. Overridden at build time via -ldflags "-X ...".
var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

// Info returns the current build information.
func Info() map[string]string {
	return map[string]string{
		"version":     Version,
		"commit":      Commit,
		"build_date":  BuildDate,
		"go_version":  runtime.Version(),
		"api_version": "v1",
	}
}
