package cli_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

// BenchmarkHookProcess includes OS process startup and the complete hidden CLI
// handler, unlike the lower-level ingest benchmarks.
func BenchmarkHookProcess(b *testing.B) {
	root := b.TempDir()
	runBenchmarkGit(b, root, "init", "--quiet")
	runBenchmarkGit(b, root, "config", "user.name", "why-diff benchmark")
	runBenchmarkGit(b, root, "config", "user.email", "benchmark@example.com")
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		b.Fatal(err)
	}
	runBenchmarkGit(b, root, "add", "main.go")
	runBenchmarkGit(b, root, "commit", "--quiet", "-m", "initial")
	binary := filepath.Join(b.TempDir(), "why-diff-hook")
	build := exec.Command("go", "build", "-trimpath", "-ldflags=-s -w", "-o", binary, "github.com/prsuyal/why-diff/cmd/why-diff-hook")
	if output, err := build.CombinedOutput(); err != nil {
		b.Fatalf("build benchmark CLI: %v: %s", err, output)
	}

	benchmarks := []struct {
		name    string
		payload string
	}{
		{"prompt", fmt.Sprintf(`{"session_id":"process-prompt","turn_id":"turn-1","cwd":%q,"hook_event_name":"UserPromptSubmit","prompt":"Explain the change"}`, root)},
		{"checkpoint", fmt.Sprintf(`{"session_id":"process-checkpoint","turn_id":"turn-1","cwd":%q,"hook_event_name":"PreToolUse","tool_name":"Bash","tool_use_id":"call-1","tool_input":{"command":"go test ./..."}}`, root)},
	}
	for _, benchmark := range benchmarks {
		b.Run(benchmark.name, func(b *testing.B) {
			codexHome := filepath.Join(b.TempDir(), "codex")
			var durations []time.Duration
			b.ResetTimer()
			for b.Loop() {
				started := time.Now()
				command := exec.Command(binary, "codex", "--strict")
				command.Env = append(os.Environ(), "CODEX_HOME="+codexHome)
				command.Stdin = bytes.NewBufferString(benchmark.payload)
				if output, err := command.CombinedOutput(); err != nil {
					b.Fatalf("hook process: %v: %s", err, output)
				}
				durations = append(durations, time.Since(started))
			}
			b.StopTimer()
			reportProcessPercentiles(b, durations)
		})
	}
}

func reportProcessPercentiles(b *testing.B, durations []time.Duration) {
	b.Helper()
	if len(durations) == 0 {
		return
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	value := func(percentile float64) float64 {
		index := int(float64(len(durations)-1) * percentile)
		return float64(durations[index].Microseconds()) / 1000
	}
	b.ReportMetric(value(0.50), "p50-ms")
	b.ReportMetric(value(0.95), "p95-ms")
}

func runBenchmarkGit(b *testing.B, root string, arguments ...string) {
	b.Helper()
	command := exec.Command("git", append([]string{"-C", root}, arguments...)...)
	if output, err := command.CombinedOutput(); err != nil {
		b.Fatalf("git %v: %v: %s", arguments, err, output)
	}
}
