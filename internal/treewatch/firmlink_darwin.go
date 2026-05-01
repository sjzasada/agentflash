//go:build darwin

package treewatch

import "strings"

// macDataVolume is the macOS firmlink target where /Users actually
// lives on APFS. FSEvents commonly emits paths under this prefix
// even when the watched root is `/Users/...`. We strip it so paths
// match the user-facing form on the way out.
const macDataVolume = "/System/Volumes/Data"

func normalizePath(p string) string {
	if strings.HasPrefix(p, macDataVolume+"/") {
		return p[len(macDataVolume):]
	}
	return p
}
