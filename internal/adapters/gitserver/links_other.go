//go:build !unix

package gitserver

import "io/fs"

// links is how many paths link the file info describes: one, where the system doesn't say.
func links(fs.FileInfo) uint64 { return 1 }
