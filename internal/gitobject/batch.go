// Package gitobject reads Git objects efficiently through one cat-file batch
// process instead of spawning one Git process per checkpoint path.
package gitobject

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
)

type Object struct {
	Spec    string
	OID     string
	Type    string
	Data    []byte
	Missing bool
}

func ReadBatch(ctx context.Context, repositoryRoot string, specs []string) ([]Object, error) {
	if len(specs) == 0 {
		return nil, nil
	}
	command := exec.CommandContext(ctx, "git", "-C", repositoryRoot, "cat-file", "--batch", "-Z")
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open git cat-file input: %w", err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("open git cat-file output: %w", err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("start git cat-file batch: %w", err)
	}
	writeErrors := make(chan error, 1)
	go func() {
		writer := bufio.NewWriter(stdin)
		for _, spec := range specs {
			if _, err := writer.WriteString(spec + "\x00"); err != nil {
				writeErrors <- err
				_ = stdin.Close()
				return
			}
		}
		err := writer.Flush()
		if closeErr := stdin.Close(); err == nil {
			err = closeErr
		}
		writeErrors <- err
	}()

	reader := bufio.NewReader(stdout)
	objects := make([]Object, 0, len(specs))
	for _, spec := range specs {
		header, err := reader.ReadString('\x00')
		if err != nil {
			_ = command.Wait()
			return nil, fmt.Errorf("read git object header for %s: %w", spec, err)
		}
		header = strings.TrimSuffix(header, "\x00")
		if strings.HasSuffix(header, " missing") {
			objects = append(objects, Object{Spec: spec, Missing: true})
			continue
		}
		fields := strings.Fields(header)
		if len(fields) != 3 {
			_ = command.Wait()
			return nil, fmt.Errorf("unexpected git object header %q", header)
		}
		size, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil || size < 0 {
			_ = command.Wait()
			return nil, fmt.Errorf("invalid git object size in %q", header)
		}
		data := make([]byte, size)
		if _, err := io.ReadFull(reader, data); err != nil {
			_ = command.Wait()
			return nil, fmt.Errorf("read git object %s: %w", fields[0], err)
		}
		terminator, err := reader.ReadByte()
		if err != nil || terminator != '\x00' {
			_ = command.Wait()
			return nil, fmt.Errorf("read git object terminator for %s", fields[0])
		}
		objects = append(objects, Object{Spec: spec, OID: fields[0], Type: fields[1], Data: data})
	}
	if err := <-writeErrors; err != nil {
		_ = command.Wait()
		return nil, fmt.Errorf("write git cat-file requests: %w", err)
	}
	if err := command.Wait(); err != nil {
		return nil, fmt.Errorf("git cat-file batch: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return objects, nil
}
