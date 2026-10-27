// Command devhub is DevHub's one binary. Each workload is a subcommand of it, so every workload runs the same build at
// one version. It only coordinates startup and shutdown: the logic lives in internal/.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/arikkfir-org/devhub/internal/system/buildinfo"
)

const usage = "usage: devhub version | api-server"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

// run runs the subcommand args name until it ends or ctx does, and returns the process's exit code: 0 on success, 1
// when the subcommand fails, 2 on a command line it does not know.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		_, _ = fmt.Fprintln(stderr, usage) // stderr is gone; nothing to tell
		return 2
	}
	switch args[0] {
	case "version":
		// The version the binary was built as, so CI and operators can tell which build runs.
		_, _ = fmt.Fprintln(stdout, buildinfo.Version()) // stdout is gone; nothing to tell
		return 0
	case "api-server":
		if err := runAPIServer(ctx, stdout); err != nil {
			_, _ = fmt.Fprintln(stderr, "devhub api-server:", err) // stderr is gone; nothing to tell
			return 1
		}
		return 0
	default:
		_, _ = fmt.Fprintln(stderr, usage) // stderr is gone; nothing to tell
		return 2
	}
}
