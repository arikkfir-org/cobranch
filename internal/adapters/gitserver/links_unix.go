//go:build unix

package gitserver

import (
	"io/fs"
	"syscall"
)

// links is how many paths link the file info describes.
func links(info fs.FileInfo) uint64 {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Nlink) //nolint:unconvert // a uint16 on some systems
	}
	return 1
}
