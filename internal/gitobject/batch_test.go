package gitobject_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/prsuyal/why-diff/internal/gitobject"
)

func TestReadBatchReturnsObjectsAndMissingPaths(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	git(t, root, "init", "--quiet")
	git(t, root, "config", "user.name", "why-diff test")
	git(t, root, "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(root, "hello world.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "line\nbreak.txt"), []byte("newline\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, root, "add", ".")
	git(t, root, "commit", "--quiet", "-m", "initial")
	tree := git(t, root, "show", "-s", "--format=%T", "HEAD")
	objects, err := gitobject.ReadBatch(context.Background(), root, []string{
		tree + ":hello world.txt", tree + ":missing.txt", tree + ":line\nbreak.txt",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(objects) != 3 || objects[0].OID == "" || objects[0].Type != "blob" || string(objects[0].Data) != "hello\n" {
		t.Fatalf("objects = %+v", objects)
	}
	if !objects[1].Missing {
		t.Fatalf("missing object = %+v", objects[1])
	}
	if string(objects[2].Data) != "newline\n" {
		t.Fatalf("newline path object = %+v", objects[2])
	}
}

func git(t *testing.T, root string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", root}, arguments...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", arguments, err, output)
	}
	return strings.TrimSpace(string(output))
}
