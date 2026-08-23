// Package cli defines WhyDiff's command-line interface.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/prsuyal/why-diff/internal/ingest"
	"github.com/prsuyal/why-diff/internal/initialize"
	"github.com/prsuyal/why-diff/internal/query"
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
	root.AddCommand(newSessionsCommand(environment))
	root.AddCommand(newShowCommand(environment))
	root.AddCommand(newWhyCommand(environment))
	root.AddCommand(newDiffCommand(environment))
	root.AddCommand(newFinalizeCommand(environment))
	root.AddCommand(newInternalCommand())
	return root
}

func newDiffCommand(environment Environment) *cobra.Command {
	return &cobra.Command{
		Use:   "diff [session]",
		Short: "Show repository changes observed around tool calls",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			selector := "latest"
			if len(arguments) == 1 {
				selector = arguments[0]
			}
			service, err := query.New(command.Context(), environment.WorkingDirectory)
			if err != nil {
				return err
			}
			session, changes, err := service.Changes(command.Context(), selector)
			if err != nil {
				return err
			}
			fmt.Fprintf(command.OutOrStdout(), "Session: %s\n", session.ID)
			if len(changes) == 0 {
				fmt.Fprintln(command.OutOrStdout(), "No checkpointed tool calls changed repository files.")
				return nil
			}
			for index, change := range changes {
				if index > 0 {
					fmt.Fprintln(command.OutOrStdout())
				}
				fmt.Fprintf(command.OutOrStdout(), "Tool: %s", change.Tool)
				if change.ToolSummary != "" {
					fmt.Fprintf(command.OutOrStdout(), " — %s", change.ToolSummary)
				}
				fmt.Fprintln(command.OutOrStdout())
				if change.Prompt != "" {
					fmt.Fprintf(command.OutOrStdout(), "Prompt: %s\n", change.Prompt)
				}
				fmt.Fprintf(command.OutOrStdout(), "Files: %s\n\n", strings.Join(change.Files, ", "))
				fmt.Fprintln(command.OutOrStdout(), change.Patch)
			}
			return nil
		},
	}
}

func newFinalizeCommand(environment Environment) *cobra.Command {
	return &cobra.Command{
		Use:   "finalize [session]",
		Short: "Archive an active session under a private Git ref",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			selector := "latest"
			if len(arguments) == 1 {
				selector = arguments[0]
			}
			service, err := query.New(command.Context(), environment.WorkingDirectory)
			if err != nil {
				return err
			}
			archive, err := service.Finalize(command.Context(), selector)
			if err != nil {
				return err
			}
			fmt.Fprintf(command.OutOrStdout(), "Finalized session at %s\nCommit: %s\n", archive.Ref, archive.Commit)
			return nil
		},
	}
}

func newSessionsCommand(environment Environment) *cobra.Command {
	return &cobra.Command{
		Use:   "sessions",
		Short: "List captured WhyDiff sessions",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			service, err := query.New(command.Context(), environment.WorkingDirectory)
			if err != nil {
				return err
			}
			summaries, err := service.Summaries(command.Context())
			if err != nil {
				return err
			}
			if len(summaries) == 0 {
				fmt.Fprintln(command.OutOrStdout(), "No captured WhyDiff sessions.")
				return nil
			}
			writer := tabwriter.NewWriter(command.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(writer, "SESSION\tSTARTED\tEVENTS\tSTATUS\tFIRST PROMPT")
			for _, summary := range summaries {
				status := "active"
				if summary.Ended {
					status = "ended"
				}
				fmt.Fprintf(writer, "%s\t%s\t%d\t%s\t%s\n",
					summary.ID,
					summary.StartedAt.Local().Format(time.RFC3339),
					summary.EventCount,
					status,
					truncate(summary.Prompt, 72),
				)
			}
			return writer.Flush()
		},
	}
}

func newShowCommand(environment Environment) *cobra.Command {
	return &cobra.Command{
		Use:   "show [session]",
		Short: "Show the observed timeline for a captured session",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			selector := "latest"
			if len(arguments) == 1 {
				selector = arguments[0]
			}
			service, err := query.New(command.Context(), environment.WorkingDirectory)
			if err != nil {
				return err
			}
			session, err := service.Session(command.Context(), selector)
			if err != nil {
				return err
			}
			fmt.Fprintf(command.OutOrStdout(), "Session: %s\n", session.ID)
			fmt.Fprintf(command.OutOrStdout(), "Events:  %d\n\n", len(session.Events))
			for _, captured := range session.Events {
				checkpointMark := ""
				if captured.Checkpoint != nil {
					checkpointMark = " [checkpoint]"
				}
				fmt.Fprintf(command.OutOrStdout(), "[%04d] %s  %s%s\n",
					captured.Sequence,
					captured.ObservedAt.Local().Format("15:04:05.000"),
					query.DescribeEvent(captured),
					checkpointMark,
				)
				for _, warning := range captured.Capture.Warnings {
					fmt.Fprintf(command.OutOrStdout(), "       warning: %s\n", warning.Code)
				}
			}
			return nil
		},
	}
}

func newWhyCommand(environment Environment) *cobra.Command {
	var sessionSelector string
	command := &cobra.Command{
		Use:   "why <file[:line]>",
		Short: "Explain which captured tool call changed a file or line",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			service, err := query.New(command.Context(), environment.WorkingDirectory)
			if err != nil {
				return err
			}
			attribution, err := service.Why(command.Context(), arguments[0], sessionSelector)
			if err != nil {
				return err
			}

			target := attribution.Target
			if attribution.Line > 0 {
				target = fmt.Sprintf("%s:%d", target, attribution.Line)
			}
			fmt.Fprintf(command.OutOrStdout(), "Target:  %s\n", target)
			fmt.Fprintf(command.OutOrStdout(), "Session: %s\n", attribution.SessionID)
			if attribution.Prompt != "" {
				fmt.Fprintf(command.OutOrStdout(), "Prompt:  %s\n", attribution.Prompt)
			}
			fmt.Fprintf(command.OutOrStdout(), "Tool:    %s", attribution.Tool)
			if attribution.ToolSummary != "" {
				fmt.Fprintf(command.OutOrStdout(), " — %s", attribution.ToolSummary)
			}
			fmt.Fprintln(command.OutOrStdout())
			fmt.Fprintln(command.OutOrStdout(), "\nEvidence:")
			fmt.Fprintf(command.OutOrStdout(), "- Tool started:   %s\n", attribution.StartedEventID)
			fmt.Fprintf(command.OutOrStdout(), "- Tool completed: %s\n", attribution.CompletedEventID)
			fmt.Fprintf(command.OutOrStdout(), "- Before tree:    %s\n", attribution.BeforeTree)
			fmt.Fprintf(command.OutOrStdout(), "- After tree:     %s\n", attribution.AfterTree)
			fmt.Fprintln(command.OutOrStdout(), "\nInference: the target changed between checkpoints immediately before and after this tool call. This is strong temporal evidence, not proof of exclusive causation.")
			fmt.Fprintln(command.OutOrStdout(), "\nPatch:")
			fmt.Fprintln(command.OutOrStdout(), attribution.Patch)
			return nil
		},
	}
	command.Flags().StringVar(&sessionSelector, "session", "", "restrict attribution to a session id or unique prefix")
	return command
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

func truncate(value string, maximum int) string {
	value = strings.Join(strings.Fields(value), " ")
	if len(value) <= maximum {
		return value
	}
	return value[:maximum-1] + "…"
}
