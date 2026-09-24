// Package hookcli is the minimal process boundary used by agent hooks. It is
// intentionally independent of Cobra, SQLite, Tree-sitter, and semantic APIs.
package hookcli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/prsuyal/why-diff/internal/event"
	"github.com/prsuyal/why-diff/internal/ingest"
	"github.com/prsuyal/why-diff/internal/initialize"
	"github.com/prsuyal/why-diff/internal/repository"
)

const maxInputBytes = 16 * 1024 * 1024

type captureFunc func(context.Context, []byte, ingest.Options) (event.Event, error)

func Run(ctx context.Context, args []string, stdin io.Reader, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: why-diff-hook codex [flags]")
		return 2
	}
	var capture captureFunc
	var provider initialize.Provider
	switch args[0] {
	case "codex":
		capture = ingest.Codex
		provider = initialize.ProviderCodex
	default:
		fmt.Fprintf(stderr, "why-diff-hook: unsupported provider %q\n", args[0])
		return 2
	}
	flags := flag.NewFlagSet("why-diff-hook "+args[0], flag.ContinueOnError)
	flags.SetOutput(stderr)
	strict := flags.Bool("strict", false, "return non-zero when capture fails")
	global := flags.Bool("global", false, "hook is installed in user settings")
	storeRoot := flags.String("store-root", "", "override the why-diff data root")
	lockTimeout := flags.Duration("lock-timeout", 500*time.Millisecond, "session lock timeout")
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "why-diff-hook: unexpected positional arguments")
		return 2
	}
	raw, err := io.ReadAll(io.LimitReader(stdin, maxInputBytes+1))
	if err == nil && len(raw) > maxInputBytes {
		err = errors.New("hook payload exceeds 16 MiB capture limit")
	}
	if err == nil && !*global && *storeRoot == "" {
		_, configured, inspectErr := initialize.GlobalHookConfigured(provider)
		if inspectErr == nil && configured {
			return 0
		}
	}
	if err == nil {
		_, err = capture(ctx, raw, ingest.Options{StoreRoot: *storeRoot, LockTimeout: *lockTimeout})
	}
	if err == nil {
		return 0
	}
	if *global && errors.Is(err, repository.ErrNotRepository) {
		return 0
	}
	fmt.Fprintf(stderr, "why-diff: capture warning: %v\n", err)
	if *strict {
		return 1
	}
	return 0
}
