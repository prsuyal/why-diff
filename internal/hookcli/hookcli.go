// Package hookcli is the minimal process boundary used by agent hooks. It is
// intentionally independent of Cobra, SQLite, Tree-sitter, and semantic APIs.
package hookcli

import (
	"context"
	"encoding/json"
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
	return RunWithOutput(ctx, args, stdin, io.Discard, stderr)
}

func RunWithOutput(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: why-diff-hook <codex|claude|cursor|gemini|copilot> [flags]")
		return 2
	}
	var capture captureFunc
	var provider initialize.Provider
	switch args[0] {
	case "codex":
		capture = ingest.Codex
		provider = initialize.ProviderCodex
	case "claude":
		capture, provider = ingest.Claude, initialize.ProviderClaude
	case "cursor":
		capture, provider = ingest.Cursor, initialize.ProviderCursor
	case "gemini":
		capture, provider = ingest.Gemini, initialize.ProviderGemini
	case "copilot":
		capture, provider = ingest.Copilot, initialize.ProviderCopilot
	default:
		fmt.Fprintf(stderr, "why-diff-hook: unsupported provider %q\n", args[0])
		return 2
	}
	flagArgs := args[1:]
	hookEvent := ""
	if (provider == initialize.ProviderCopilot || provider == initialize.ProviderCursor) && len(flagArgs) > 0 && len(flagArgs[0]) > 0 && flagArgs[0][0] != '-' {
		hookEvent, flagArgs = flagArgs[0], flagArgs[1:]
	}
	flags := flag.NewFlagSet("why-diff-hook "+args[0], flag.ContinueOnError)
	flags.SetOutput(stderr)
	strict := flags.Bool("strict", false, "return non-zero when capture fails")
	global := flags.Bool("global", false, "hook is installed in user settings")
	storeRoot := flags.String("store-root", "", "override the why-diff data root")
	lockTimeout := flags.Duration("lock-timeout", 500*time.Millisecond, "session lock timeout")
	if err := flags.Parse(flagArgs); err != nil {
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
	if err == nil && hookEvent != "" {
		var object map[string]json.RawMessage
		if json.Unmarshal(raw, &object) == nil && object != nil {
			if _, present := object["hook_event_name"]; !present {
				object["hook_event_name"], _ = json.Marshal(hookEvent)
				raw, err = json.Marshal(object)
			}
		}
	}
	if err == nil && !*global && *storeRoot == "" {
		_, configured, inspectErr := initialize.GlobalHookConfigured(provider)
		if inspectErr == nil && configured {
			writeAcknowledgement(stdout, provider, raw, hookEvent)
			return 0
		}
	}
	if err == nil {
		_, err = capture(ctx, raw, ingest.Options{StoreRoot: *storeRoot, LockTimeout: *lockTimeout})
	}
	if err == nil {
		writeAcknowledgement(stdout, provider, raw, hookEvent)
		return 0
	}
	if *global && errors.Is(err, repository.ErrNotRepository) {
		writeAcknowledgement(stdout, provider, raw, hookEvent)
		return 0
	}
	fmt.Fprintf(stderr, "why-diff: capture warning: %v\n", err)
	if *strict {
		return 1
	}
	writeAcknowledgement(stdout, provider, raw, hookEvent)
	return 0
}

func writeAcknowledgement(stdout io.Writer, provider initialize.Provider, raw []byte, hookEvent string) {
	switch provider {
	case initialize.ProviderCursor:
		var input struct {
			HookEventName string `json:"hook_event_name"`
		}
		_ = json.Unmarshal(raw, &input)
		if hookEvent == "" {
			hookEvent = input.HookEventName
		}
		switch hookEvent {
		case "preToolUse":
			fmt.Fprintln(stdout, `{"permission":"ask"}`)
		default:
			fmt.Fprintln(stdout, `{}`)
		}
	case initialize.ProviderGemini, initialize.ProviderCopilot:
		fmt.Fprintln(stdout, `{}`)
	}
}
