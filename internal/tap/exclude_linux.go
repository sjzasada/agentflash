//go:build linux

package tap

// excludeProcesses is the set of Linux background daemons whose
// activity we drop by default. These are common indexers / journalers
// / desktop helpers that read files constantly and would drown out
// user activity in the timeline.
//
// /proc/<pid>/comm truncates at 15 characters, so each entry below
// matches what the kernel will report rather than the full binary
// name. Add more via --exclude-name at runtime.
var excludeProcesses = map[string]struct{}{
	"systemd-journal":  {}, // truncated systemd-journald
	"tracker-miner-f":  {}, // truncated tracker-miner-fs-3
	"tracker-extract":  {},
	"updatedb":         {},
	"updatedb.mlocat":  {},
	"mlocate":          {},
	"plocate":          {},
	"baloo_file":       {},
	"baloo_file_extr":  {},
	"gvfsd":            {},
	"gvfsd-trash":      {},
	"gvfsd-metadata":   {},
	"thumbnail":        {}, // gnome thumbnail factories
	"thumbnailer":      {},
	"snapd":            {},
	"packagekitd":      {},
}
