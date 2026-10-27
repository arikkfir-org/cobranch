// Command devhub is DevHub's one binary. Each workload is a subcommand of it, so every workload runs the same build at
// one version. It only coordinates startup: the logic lives in internal/.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/arikkfir-org/devhub/internal/system/buildinfo"
)

const usage = "usage: devhub version"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run runs the subcommand args name and returns the process's exit code: 0 on success, 2 on a command line it does not
// know. "version" prints the version the binary was built as, so CI and operators can tell which build runs.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 || args[0] != "version" {
		_, _ = fmt.Fprintln(stderr, usage) // stderr is gone; nothing to tell
		return 2
	}
	_, _ = fmt.Fprintln(stdout, buildinfo.Version()) // stdout is gone; nothing to tell
	return 0
}
