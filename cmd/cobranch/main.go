// Command cobranch is Cobranch's one binary. Each workload is a subcommand of it, so every workload runs the same build
// at one version. It only coordinates startup and shutdown: the logic lives in internal/.
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"cobranch.dev/internal/system/buildinfo"
)

const usage = "usage: cobranch version | migrate | auth-server | rest-server | graphql-server | admin-server | maintenance"

// telemetryFlushTimeout bounds how long a workload waits, as it exits, for its last spans to be exported.
const telemetryFlushTimeout = 5 * time.Second

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
	var err error
	switch args[0] {
	case "version":
		// The version the binary was built as, so CI and operators can tell which build runs.
		_, _ = fmt.Fprintln(stdout, buildinfo.Version()) // stdout is gone; nothing to tell
		return 0
	case "migrate":
		err = runMigrate(ctx, stdout)
	case "auth-server":
		err = runAuthServer(ctx, stdout)
	case "maintenance":
		err = runMaintenance(ctx, stdout)
	case restServer.workload:
		err = runServer(ctx, stdout, restServer)
	case graphqlServer.workload:
		err = runServer(ctx, stdout, graphqlServer)
	case adminServer.workload:
		err = runServer(ctx, stdout, adminServer)
	default:
		_, _ = fmt.Fprintln(stderr, usage) // stderr is gone; nothing to tell
		return 2
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "cobranch %s: %v\n", args[0], err) // stderr is gone; nothing to tell
		return 1
	}
	return 0
}

// flushTelemetry exports the spans still buffered, giving up after telemetryFlushTimeout even when ctx, which a
// shutdown ends, has ended. A failure is only logged: the workload's own outcome stands.
func flushTelemetry(ctx context.Context, flush func(context.Context) error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), telemetryFlushTimeout)
	defer cancel()
	if err := flush(ctx); err != nil {
		slog.WarnContext(ctx, "Exporting the last spans failed", "error", err)
	}
}
