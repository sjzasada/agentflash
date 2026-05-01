//go:build linux

package treewatch

// On Linux there's no equivalent of macOS firmlinks; inotify reports
// the actual path the user passed to FanotifyMark/InotifyAddWatch, so
// no normalization is needed.
func normalizePath(p string) string { return p }
