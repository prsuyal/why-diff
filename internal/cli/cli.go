// Package cli defines WhyDiff's command-line interface.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/prsuyal/why-diff/internal/ingest"
	"github.com/prsuyal/why-diff/internal/initialize"
	"github.com/prsuyal/why-diff/internal/query"
	"github.com/prsuyal/why-diff/internal/semantic"
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
	root.AddCommand(newClaimsCommand(environment))
	root.AddCommand(newExplainCommand(environment))
	root.AddCommand(newCompareCommand(environment))
	root.AddCommand(newFinalizeCommand(environment))
	root.AddCommand(newInternalCommand())
	return root
}

func newCompareCommand(environment Environment) *cobra.Command {
	var showPatches bool
	var semanticInterpretation bool
	var dryRun bool
	var model string
	var timeout time.Duration
	command := &cobra.Command{
		Use:   "compare <session-a> <session-b>",
		Short: "Compare two captured attempts using observed evidence",
		Args:  cobra.ExactArgs(2),
		RunE: func(command *cobra.Command, arguments []string) error {
			service, err := query.New(command.Context(), environment.WorkingDirectory)
			if err != nil {
				return err
			}
			var comparison query.Comparison
			var packet semantic.EvidencePacket
			if semanticInterpretation || dryRun {
				comparison, packet, err = service.ComparisonSemanticEvidence(command.Context(), arguments[0], arguments[1])
			} else {
				comparison, err = service.Compare(command.Context(), arguments[0], arguments[1])
			}
			if err != nil {
				return err
			}
			if dryRun {
				return printSemanticPacket(command.OutOrStdout(), packet)
			}

			fmt.Fprintln(command.OutOrStdout(), "Evidence-backed session comparison (observations, not semantic conclusions)")
			printComparisonAttempt(command.OutOrStdout(), "A", comparison.Left, showPatches)
			printComparisonAttempt(command.OutOrStdout(), "B", comparison.Right, showPatches)
			fmt.Fprintln(command.OutOrStdout(), "\nChanged files:")
			printComparisonList(command.OutOrStdout(), "Shared", comparison.SharedFiles)
			printComparisonList(command.OutOrStdout(), "Only A", comparison.LeftOnlyFiles)
			printComparisonList(command.OutOrStdout(), "Only B", comparison.RightOnlyFiles)
			fmt.Fprintln(command.OutOrStdout(), "\nValidation commands:")
			printComparisonList(command.OutOrStdout(), "Shared", comparison.SharedValidations)
			printComparisonList(command.OutOrStdout(), "Only A", comparison.LeftOnlyValidations)
			printComparisonList(command.OutOrStdout(), "Only B", comparison.RightOnlyValidations)
			fmt.Fprintln(command.OutOrStdout(), "\nInference boundary: overlap and divergence are observed; WhyDiff has not inferred why the attempts differ.")
			if semanticInterpretation {
				generator, err := newSemanticGenerator(model)
				if err != nil {
					return err
				}
				requestContext, cancel := context.WithTimeout(command.Context(), timeout)
				defer cancel()
				explanation, err := generator.Explain(requestContext, packet)
				if err != nil {
					return err
				}
				fmt.Fprintln(command.OutOrStdout())
				printSemanticExplanation(command.OutOrStdout(), packet, explanation)
			}
			return nil
		},
	}
	command.Flags().BoolVar(&showPatches, "patch", false, "include each attempt's checkpoint patches")
	command.Flags().BoolVar(&semanticInterpretation, "explain", false, "generate a model interpretation of the bounded comparison evidence")
	command.Flags().BoolVar(&dryRun, "dry-run", false, "print the comparison evidence packet without making an API request")
	command.Flags().StringVar(&model, "model", "", "OpenAI model (default: WHYDIFF_OPENAI_MODEL or "+semantic.DefaultModel+")")
	command.Flags().DurationVar(&timeout, "timeout", 30*time.Second, "maximum time for the semantic API request")
	return command
}

func printComparisonAttempt(writer io.Writer, label string, attempt query.ComparisonAttempt, showPatches bool) {
	fmt.Fprintf(writer, "\nAttempt %s\n", label)
	fmt.Fprintf(writer, "Session: %s\n", attempt.SessionID)
	if len(attempt.Prompts) == 0 {
		fmt.Fprintln(writer, "Prompts: (none captured)")
	} else {
		fmt.Fprintln(writer, "Prompts:")
		for _, prompt := range attempt.Prompts {
			fmt.Fprintf(writer, "- %s [%s]\n", prompt.Text, prompt.EventID)
		}
	}
	fmt.Fprintf(writer, "Checkpointed changes: %d\n", len(attempt.Changes))
	if len(attempt.Validations) == 0 {
		fmt.Fprintln(writer, "Observed validations: (none)")
	} else {
		fmt.Fprintln(writer, "Observed validations:")
		for _, validation := range attempt.Validations {
			fmt.Fprintf(writer, "- %s: %s [%s]\n", validation.Outcome, validation.Command, validation.EventID)
		}
	}
	if showPatches {
		for _, change := range attempt.Changes {
			fmt.Fprintf(writer, "\nPatch [%s -> %s]:\n%s\n", change.StartedEventID, change.CompletedEventID, change.Patch)
		}
	}
}

func printComparisonList(writer io.Writer, label string, values []string) {
	if len(values) == 0 {
		fmt.Fprintf(writer, "- %s: (none)\n", label)
		return
	}
	fmt.Fprintf(writer, "- %s: %s\n", label, strings.Join(values, ", "))
}

func newExplainCommand(environment Environment) *cobra.Command {
	var sessionSelector string
	var model string
	var dryRun bool
	var timeout time.Duration
	command := &cobra.Command{
		Use:   "explain <file[:line]>",
		Short: "Generate an optional model interpretation from bounded evidence",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			service, err := query.New(command.Context(), environment.WorkingDirectory)
			if err != nil {
				return err
			}
			_, packet, err := service.SemanticEvidence(command.Context(), arguments[0], sessionSelector)
			if err != nil {
				return err
			}
			if dryRun {
				return printSemanticPacket(command.OutOrStdout(), packet)
			}

			generator, err := newSemanticGenerator(model)
			if err != nil {
				return err
			}
			requestContext, cancel := context.WithTimeout(command.Context(), timeout)
			defer cancel()
			explanation, err := generator.Explain(requestContext, packet)
			if err != nil {
				return err
			}

			printSemanticExplanation(command.OutOrStdout(), packet, explanation)
			fmt.Fprintln(command.OutOrStdout(), "\nUse `whydiff why` and `whydiff claims` for the underlying deterministic evidence.")
			return nil
		},
	}
	command.Flags().StringVar(&sessionSelector, "session", "", "restrict evidence to a session id or unique prefix")
	command.Flags().StringVar(&model, "model", "", "OpenAI model (default: WHYDIFF_OPENAI_MODEL or "+semantic.DefaultModel+")")
	command.Flags().BoolVar(&dryRun, "dry-run", false, "print the evidence packet without making an API request")
	command.Flags().DurationVar(&timeout, "timeout", 30*time.Second, "maximum time for the semantic API request")
	return command
}

func printSemanticPacket(writer io.Writer, packet semantic.EvidencePacket) error {
	encoded, err := json.MarshalIndent(packet, "", "  ")
	if err != nil {
		return fmt.Errorf("encode semantic evidence preview: %w", err)
	}
	fmt.Fprintln(writer, string(encoded))
	return nil
}

func newSemanticGenerator(model string) (*semantic.OpenAI, error) {
	if model == "" {
		model = os.Getenv("WHYDIFF_OPENAI_MODEL")
	}
	generator, err := semantic.NewOpenAI(semantic.OpenAIConfig{
		APIKey:  os.Getenv("OPENAI_API_KEY"),
		Model:   model,
		BaseURL: os.Getenv("OPENAI_BASE_URL"),
	})
	if err != nil {
		return nil, fmt.Errorf("configure semantic provider: %w; use --dry-run to inspect exactly what would be sent", err)
	}
	return generator, nil
}

func printSemanticExplanation(writer io.Writer, packet semantic.EvidencePacket, explanation semantic.Explanation) {
	fmt.Fprintln(writer, "Model-generated interpretation (not an observed fact)")
	fmt.Fprintf(writer, "Target:   %s\n", packet.Target)
	fmt.Fprintf(writer, "Sessions: %s\n", strings.Join(packet.SessionIDs, ", "))
	fmt.Fprintf(writer, "Provider: %s\n", explanation.Provider)
	fmt.Fprintf(writer, "Model:    %s\n", explanation.Model)
	fmt.Fprintf(writer, "Response: %s\n\n", explanation.ResponseID)
	fmt.Fprintln(writer, explanation.Summary)
	if len(explanation.Claims) > 0 {
		fmt.Fprintln(writer, "\nClaims:")
		for _, claim := range explanation.Claims {
			fmt.Fprintf(writer, "- [%s] %s\n", claim.Confidence, claim.Statement)
			fmt.Fprintf(writer, "  Evidence: %s\n", strings.Join(claim.EvidenceIDs, ", "))
			if claim.Qualification != "" {
				fmt.Fprintf(writer, "  Qualification: %s\n", claim.Qualification)
			}
		}
	}
	if len(explanation.Unknowns) > 0 {
		fmt.Fprintln(writer, "\nUnknowns:")
		for _, unknown := range explanation.Unknowns {
			fmt.Fprintf(writer, "- %s\n", unknown)
		}
	}
}

func newClaimsCommand(environment Environment) *cobra.Command {
	return &cobra.Command{
		Use:   "claims [session]",
		Short: "Show deterministic claims derived from captured evidence",
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
			session, claims, err := service.Claims(command.Context(), selector)
			if err != nil {
				return err
			}
			fmt.Fprintf(command.OutOrStdout(), "Session: %s\n", session.ID)
			if len(claims) == 0 {
				fmt.Fprintln(command.OutOrStdout(), "No deterministic fail-change-pass claims were found.")
				return nil
			}
			for index, claim := range claims {
				if index > 0 {
					fmt.Fprintln(command.OutOrStdout())
				}
				fmt.Fprintf(command.OutOrStdout(), "Claim: %s\n", claim.ClaimID)
				fmt.Fprintf(command.OutOrStdout(), "Rule: %s\n", claim.RuleID)
				fmt.Fprintln(command.OutOrStdout(), "Conclusion: repository changes are consistent with resolving a test failure.")
				fmt.Fprintf(command.OutOrStdout(), "Command: %s\n", claim.Command)
				fmt.Fprintf(command.OutOrStdout(), "Files: %s\n", strings.Join(claim.Files, ", "))
				fmt.Fprintln(command.OutOrStdout(), "Evidence:")
				fmt.Fprintf(command.OutOrStdout(), "- Failed:  %s (%s)\n", claim.FailedEventID, claim.FailedBasis)
				fmt.Fprintf(command.OutOrStdout(), "- Changes: %s\n", strings.Join(claim.ChangeEventIDs, ", "))
				fmt.Fprintf(command.OutOrStdout(), "- Passed:  %s (%s)\n", claim.PassedEventID, claim.PassedBasis)
				fmt.Fprintln(command.OutOrStdout(), "Caveat: this temporal sequence supports the claim but does not prove exclusive causation.")
			}
			return nil
		},
	}
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
			if validation := attribution.Validation; validation != nil {
				fmt.Fprintln(command.OutOrStdout(), "\nValidation:")
				fmt.Fprintf(command.OutOrStdout(), "- `%s` failed before the change: %s (%s)\n", validation.Command, validation.FailedEventID, validation.FailedBasis)
				fmt.Fprintf(command.OutOrStdout(), "- The same command passed afterward: %s (%s)\n", validation.PassedEventID, validation.PassedBasis)
				fmt.Fprintln(command.OutOrStdout(), "- This is consistent with the captured changes resolving the failure; it is not proof that every changed line was necessary.")
			}
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
