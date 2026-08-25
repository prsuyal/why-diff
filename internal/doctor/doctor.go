// Package doctor diagnoses whether repository-local WhyDiff capture is usable.
package doctor

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/prsuyal/why-diff/internal/initialize"
	"github.com/prsuyal/why-diff/internal/provenance"
	"github.com/prsuyal/why-diff/internal/repository"
	"github.com/prsuyal/why-diff/internal/store"
)

type Status string

const (
	StatusOK      Status = "ok"
	StatusWarning Status = "warn"
	StatusError   Status = "error"
)

type Check struct {
	Name   string
	Status Status
	Detail string
}

type Report struct {
	Checks []Check
}

func (r Report) Ready() bool {
	for _, check := range r.Checks {
		if check.Status == StatusError {
			return false
		}
	}
	return true
}

type Options struct {
	LookupExecutable func(string) (string, error)
}

func Run(ctx context.Context, cwd string, options Options) Report {
	var report Report
	location, err := repository.Locate(ctx, cwd)
	if err != nil {
		report.Checks = append(report.Checks, Check{
			Name:   "Git repository",
			Status: StatusError,
			Detail: err.Error(),
		})
		return report
	}
	report.Checks = append(report.Checks, Check{
		Name:   "Git repository",
		Status: StatusOK,
		Detail: location.WorktreeRoot,
	})

	inspection, err := initialize.Inspect(ctx, cwd)
	if err != nil {
		report.Checks = append(report.Checks, Check{
			Name:   "Project configuration",
			Status: StatusError,
			Detail: err.Error(),
		})
	} else {
		marker := Check{Name: "Project marker", Status: StatusOK, Detail: inspection.MarkerPath}
		if !inspection.MarkerValid {
			marker.Status = StatusError
			marker.Detail = "missing or unsupported; run `whydiff init`"
		}
		report.Checks = append(report.Checks, marker)

		hooks := Check{Name: "Codex hooks", Status: StatusOK, Detail: inspection.HooksPath}
		if !inspection.HooksValid {
			hooks.Status = StatusWarning
			hooks.Detail = "not configured; run `whydiff init --provider codex` to enable"
		}
		report.Checks = append(report.Checks, hooks)

		claudeHooks := Check{Name: "Claude Code hooks", Status: StatusOK, Detail: inspection.ClaudeHooksPath}
		if !inspection.ClaudeHooksValid {
			claudeHooks.Status = StatusWarning
			claudeHooks.Detail = "not configured; run `whydiff init --provider claude` to enable"
		}
		report.Checks = append(report.Checks, claudeHooks)
		if !inspection.HooksValid && !inspection.ClaudeHooksValid {
			report.Checks = append(report.Checks, Check{
				Name: "Agent hooks", Status: StatusError,
				Detail: "no supported agent integration is configured; run `whydiff init`",
			})
		}
	}

	lookup := options.LookupExecutable
	if lookup == nil {
		lookup = exec.LookPath
	}
	executable, err := lookup("whydiff")
	if err != nil {
		report.Checks = append(report.Checks, Check{
			Name:   "Hook executable",
			Status: StatusError,
			Detail: "`whydiff` is not on PATH; configured agents cannot run the generated hook command",
		})
	} else {
		report.Checks = append(report.Checks, Check{
			Name:   "Hook executable",
			Status: StatusOK,
			Detail: executable,
		})
		if current, currentErr := os.Executable(); currentErr == nil {
			if same, compareErr := sameExecutable(current, executable); compareErr == nil && !same {
				report.Checks = append(report.Checks, Check{
					Name:   "Development binary",
					Status: StatusWarning,
					Detail: "the running binary differs from the binary on PATH used by hooks; rebuild the PATH binary before testing",
				})
			}
		}
	}

	dataRoot := repository.DataRoot(location)
	live, liveErr := store.New(dataRoot).Sessions(ctx)
	archived, archiveErr := provenance.Sessions(ctx, location)
	if liveErr != nil || archiveErr != nil {
		detail := ""
		if liveErr != nil {
			detail = "live store: " + liveErr.Error()
		}
		if archiveErr != nil {
			if detail != "" {
				detail += "; "
			}
			detail += "Git archive: " + archiveErr.Error()
		}
		report.Checks = append(report.Checks, Check{Name: "Provenance data", Status: StatusError, Detail: detail})
	} else {
		status := StatusOK
		detail := fmt.Sprintf("%d live session(s), %d archived session(s)", len(live), len(archived))
		if len(live) == 0 && len(archived) == 0 {
			status = StatusWarning
			detail = "no captured sessions yet; start a fresh agent session after initialization"
		}
		report.Checks = append(report.Checks, Check{Name: "Provenance data", Status: status, Detail: detail})

		byID := make(map[string]store.Session, len(live)+len(archived))
		for _, session := range archived {
			byID[session.ID] = session
		}
		for _, session := range live {
			byID[session.ID] = session
		}
		warningCount := 0
		for _, session := range byID {
			for _, captured := range session.Events {
				warningCount += len(captured.Capture.Warnings)
			}
		}
		warningStatus := StatusOK
		warningDetail := "no capture warnings in live sessions"
		if warningCount > 0 {
			warningStatus = StatusWarning
			warningDetail = fmt.Sprintf("%d warning(s); inspect affected sessions with `whydiff show`", warningCount)
		}
		report.Checks = append(report.Checks, Check{Name: "Capture quality", Status: warningStatus, Detail: warningDetail})
	}

	quarantines, globErr := filepath.Glob(filepath.Join(dataRoot, "active", "*", "corrupt-tail-*.bin"))
	if globErr == nil && len(quarantines) > 0 {
		report.Checks = append(report.Checks, Check{
			Name:   "Crash recovery",
			Status: StatusWarning,
			Detail: fmt.Sprintf("%d preserved incomplete log tail(s) under %s", len(quarantines), dataRoot),
		})
	}
	return report
}

func sameExecutable(left, right string) (bool, error) {
	leftInfo, err := os.Stat(left)
	if err != nil {
		return false, err
	}
	rightInfo, err := os.Stat(right)
	if err != nil {
		return false, err
	}
	if os.SameFile(leftInfo, rightInfo) {
		return true, nil
	}
	leftDigest, err := fileDigest(left)
	if err != nil {
		return false, err
	}
	rightDigest, err := fileDigest(right)
	if err != nil {
		return false, err
	}
	return leftDigest == rightDigest, nil
}

func fileDigest(path string) ([sha256.Size]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return [sha256.Size]byte{}, err
	}
	var digest [sha256.Size]byte
	copy(digest[:], hash.Sum(nil))
	return digest, nil
}
