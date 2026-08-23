// Package cli defines WhyDiff's command-line interface.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/prsuyal/why-diff/internal/ingest"
	"github.com/prsuyal/why-diff/internal/initialize"
	"github.com/spf13/cobra"
)

const maxHookInputBytes = 16 * 1024 * 1024

var errStrictCapture = errors.New("strict capture failed")

type Environment struct {
	Stdin            io.Reader
	Stdout           io.Writer
	Stderr           io.Writer
	WorkingDirectory string
}

func Run(ctx context.Context, args []string, environment Environment) int {
	root := New(environment)
	root.SetArgs(args)
	if err := root.ExecuteContext(ctx); err != nil {
		if !errors.Is(err, errStrictCapture) {
			fmt.Fprintf(environment.Stderr, "whydiff: %v\n", err)
		}
		return 1
	}
	return 0
}

func New(environment Environment) *cobra.Command {
	root := &cobra.Command{
		Use:           "whydiff",
		Short:         "Explain AI-assisted code changes from captured evidence",
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	root.SetIn(environment.Stdin)
	root.SetOut(environment.Stdout)
	root.SetErr(environment.Stderr)

	root.AddCommand(newInitCommand(environment))
	root.AddCommand(newInternalCommand())
	return root
}

func newInitCommand(environment Environment) *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Initialize WhyDiff in the current Git repository",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			result, err := initialize.Run(command.Context(), environment.WorkingDirectory)
			if err != nil {
				return err
			}
			if !result.MarkerCreated && !result.HooksChanged {
				fmt.Fprintf(command.OutOrStdout(), "WhyDiff is already initialized in %s\n", result.RepositoryRoot)
				return nil
			}
			fmt.Fprintf(command.OutOrStdout(), "Initialized WhyDiff in %s\n", result.RepositoryRoot)
			if result.HooksChanged {
				fmt.Fprintf(command.OutOrStdout(), "Updated %s\n", result.HooksPath)
				fmt.Fprintln(command.OutOrStdout(), "Review and trust the project hooks with /hooks in Codex.")
			}
			return nil
		},
	}
}

func newInternalCommand() *cobra.Command {
	internal := &cobra.Command{
		Use:    "internal",
		Hidden: true,
	}
	ingestCommand := &cobra.Command{
		Use:    "ingest",
		Hidden: true,
	}
	ingestCommand.AddCommand(newCodexIngestCommand())
	internal.AddCommand(ingestCommand)
	return internal
}

func newCodexIngestCommand() *cobra.Command {
	var strict bool
	var storeRoot string
	var lockTimeout time.Duration

	command := &cobra.Command{
		Use:    "codex",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			raw, err := io.ReadAll(io.LimitReader(command.InOrStdin(), maxHookInputBytes+1))
			if err == nil && len(raw) > maxHookInputBytes {
				err = errors.New("hook payload exceeds 16 MiB capture limit")
			}
			if err == nil {
				_, err = ingest.Codex(command.Context(), raw, ingest.CodexOptions{
					StoreRoot:   storeRoot,
					LockTimeout: lockTimeout,
				})
			}
			if err == nil {
				return nil
			}

			fmt.Fprintf(command.ErrOrStderr(), "whydiff: capture warning: %v\n", err)
			if strict {
				return errStrictCapture
			}
			return nil
		},
	}
	command.Flags().BoolVar(&strict, "strict", false, "return a non-zero status when capture fails")
	command.Flags().StringVar(&storeRoot, "store-root", "", "override the WhyDiff data root")
	command.Flags().DurationVar(&lockTimeout, "lock-timeout", 500*time.Millisecond, "maximum time to wait for the session log lock")
	return command
}
