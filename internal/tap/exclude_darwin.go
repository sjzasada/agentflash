//go:build darwin

package tap

// excludeProcesses is the set of macOS background daemons whose
// activity we drop by default. They re-read files constantly
// (Spotlight indexing, fsevent notification, telemetry agents) and
// produce noise that drowns out the user's own activity.
var excludeProcesses = map[string]struct{}{
	"mds":             {},
	"mds_stores":      {},
	"mdworker":        {},
	"mdworker_shared": {},
	"fseventsd":       {},
	"BiomeAgent":      {},
	"corespotlightd":  {},
}
