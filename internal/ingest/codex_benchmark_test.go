package ingest_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/prsuyal/why-diff/internal/ingest"
)

func BenchmarkCodexPromptCapture(b *testing.B) {
	root := b.TempDir()
	raw := []byte(fmt.Sprintf(`{
  "session_id": "benchmark-prompt",
  "turn_id": "turn-1",
  "cwd": %q,
  "hook_event_name": "UserPromptSubmit",
  "prompt": "Fix the authentication timeout"
}`, root))

	b.ReportAllocs()
	b.ResetTimer()
	var durations []time.Duration
	for b.Loop() {
		started := time.Now()
		if _, err := ingest.Codex(context.Background(), raw, ingest.CodexOptions{StoreRoot: root}); err != nil {
			b.Fatal(err)
		}
		durations = append(durations, time.Since(started))
	}
	b.StopTimer()
	reportLatencyPercentiles(b, durations)
}

func BenchmarkCodexToolCheckpointCapture(b *testing.B) {
	repositoryRoot := b.TempDir()
	benchmarkGit(b, repositoryRoot, "init", "--quiet")
	benchmarkGit(b, repositoryRoot, "config", "user.name", "WhyDiff Benchmark")
	benchmarkGit(b, repositoryRoot, "config", "user.email", "benchmark@example.com")
	if err := os.WriteFile(filepath.Join(repositoryRoot, "app.go"), []byte("package app\n"), 0o644); err != nil {
		b.Fatal(err)
	}
	benchmarkGit(b, repositoryRoot, "add", "app.go")
	benchmarkGit(b, repositoryRoot, "commit", "--quiet", "-m", "initial")
	storeRoot := filepath.Join(b.TempDir(), "store")
	raw := []byte(fmt.Sprintf(`{
  "session_id": "benchmark-tool",
  "turn_id": "turn-1",
  "cwd": %q,
  "hook_event_name": "PreToolUse",
  "tool_name": "apply_patch",
  "tool_use_id": "call-1",
  "tool_input": {"command": "edit app.go"}
}`, repositoryRoot))

	b.ReportAllocs()
	b.ResetTimer()
	var durations []time.Duration
	for b.Loop() {
		started := time.Now()
		if _, err := ingest.Codex(context.Background(), raw, ingest.CodexOptions{StoreRoot: storeRoot}); err != nil {
			b.Fatal(err)
		}
		durations = append(durations, time.Since(started))
	}
	b.StopTimer()
	reportLatencyPercentiles(b, durations)
}

func reportLatencyPercentiles(b *testing.B, durations []time.Duration) {
	b.Helper()
	if len(durations) == 0 {
		return
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	percentile := func(value float64) time.Duration {
		index := int(float64(len(durations)-1) * value)
		return durations[index]
	}
	b.ReportMetric(float64(percentile(0.50).Microseconds())/1000, "p50-ms")
	b.ReportMetric(float64(percentile(0.95).Microseconds())/1000, "p95-ms")
}

func benchmarkGit(b *testing.B, root string, arguments ...string) {
	b.Helper()
	command := exec.Command("git", append([]string{"-C", root}, arguments...)...)
	if output, err := command.CombinedOutput(); err != nil {
		b.Fatalf("git %v: %v: %s", arguments, err, output)
	}
}
