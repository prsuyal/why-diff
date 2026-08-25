package query_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/prsuyal/why-diff/internal/ingest"
	"github.com/prsuyal/why-diff/internal/query"
)

// BenchmarkWhySQLiteProjection measures the same attribution query after an
// index deletion (rebuild from canonical evidence) and with a current index.
func BenchmarkWhySQLiteProjection(b *testing.B) {
	root := b.TempDir()
	benchmarkGit(b, root, "init", "--quiet")
	benchmarkGit(b, root, "config", "user.name", "WhyDiff Benchmark")
	benchmarkGit(b, root, "config", "user.email", "benchmark@example.com")
	path := filepath.Join(root, "target.go")
	if err := os.WriteFile(path, []byte("package demo\n\nfunc Target() int { return 0 }\n"), 0o644); err != nil {
		b.Fatal(err)
	}
	benchmarkGit(b, root, "add", "target.go")
	benchmarkGit(b, root, "commit", "--quiet", "-m", "initial")
	benchmarkIngest(b, root, `{"session_id":"benchmark-session","turn_id":"turn-1","cwd":%q,"hook_event_name":"UserPromptSubmit","prompt":"Iterate on Target"}`)
	for iteration := 1; iteration <= 10; iteration++ {
		toolID := fmt.Sprintf("edit-%d", iteration)
		benchmarkIngest(b, root, `{"session_id":"benchmark-session","turn_id":"turn-1","cwd":%q,"hook_event_name":"PreToolUse","tool_name":"apply_patch","tool_use_id":"`+toolID+`","tool_input":{"command":"update Target"}}`)
		contents := fmt.Sprintf("package demo\n\nfunc Target() int { return %d }\n", iteration)
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			b.Fatal(err)
		}
		benchmarkIngest(b, root, `{"session_id":"benchmark-session","turn_id":"turn-1","cwd":%q,"hook_event_name":"PostToolUse","tool_name":"apply_patch","tool_use_id":"`+toolID+`","tool_input":{"command":"update Target"},"tool_response":{"output":"Done"}}`)
	}

	service, err := query.New(context.Background(), root)
	if err != nil {
		b.Fatal(err)
	}
	stats, err := service.Index(context.Background(), true)
	if err != nil {
		b.Fatal(err)
	}

	b.Run("warm_index", func(b *testing.B) {
		for b.Loop() {
			if _, err := service.Why(context.Background(), "target.go:3", "benchmark-session"); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("cold_rebuild", func(b *testing.B) {
		for b.Loop() {
			if err := os.Remove(stats.Path); err != nil {
				b.Fatal(err)
			}
			if _, err := service.Why(context.Background(), "target.go:3", "benchmark-session"); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func benchmarkIngest(b *testing.B, root, template string) {
	b.Helper()
	raw := []byte(fmt.Sprintf(template, root))
	if _, err := ingest.Codex(context.Background(), raw, ingest.CodexOptions{ObservedAt: time.Now()}); err != nil {
		b.Fatal(err)
	}
}

func benchmarkGit(b *testing.B, root string, arguments ...string) {
	b.Helper()
	command := exec.Command("git", append([]string{"-C", root}, arguments...)...)
	if output, err := command.CombinedOutput(); err != nil {
		b.Fatalf("git %v: %v: %s", arguments, err, output)
	}
}
