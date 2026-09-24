// Package doctor diagnoses whether repository-local why-diff capture is usable.
package doctor

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/prsuyal/why-diff/internal/indexdb"
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
	batch := exec.CommandContext(ctx, "git", "-C", location.WorktreeRoot, "cat-file", "--batch", "-Z")
	batch.Stdin = strings.NewReader("")
	if err := batch.Run(); err != nil {
		report.Checks = append(report.Checks, Check{
			Name: "Git batch reads", Status: StatusError,
			Detail: "Git 2.42 or newer is required for indexed queries",
		})
	} else {
		report.Checks = append(report.Checks, Check{
			Name: "Git batch reads", Status: StatusOK,
			Detail: "NUL-delimited object reads are available",
		})
	}

	globalCodexPath, globalCodex, globalCodexErr := initialize.GlobalHookConfigured(initialize.ProviderCodex)
	if globalCodexErr != nil {
		report.Checks = append(report.Checks, Check{Name: "Global Codex hooks", Status: StatusError, Detail: globalCodexErr.Error()})
	}
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
			if globalCodex {
				marker.Status = StatusWarning
				marker.Detail = "not needed for global capture"
			} else {
				marker.Status = StatusError
				marker.Detail = "missing or unsupported; run `why-diff init` or `why-diff init --global`"
			}
		}
		report.Checks = append(report.Checks, marker)

		hooks := Check{Name: "Codex hooks", Status: StatusOK, Detail: inspection.HooksPath}
		if !inspection.HooksValid {
			if globalCodex {
				hooks.Detail = globalCodexPath + " (global)"
			} else {
				hooks.Status = StatusWarning
				hooks.Detail = "not configured; run `why-diff init` to enable"
			}
		}
		report.Checks = append(report.Checks, hooks)

		if !inspection.HooksValid && !globalCodex {
			report.Checks = append(report.Checks, Check{
				Name: "Agent hooks", Status: StatusError,
				Detail: "Codex capture is not configured; run `why-diff init`",
			})
		}
	}

	lookup := options.LookupExecutable
	if lookup == nil {
		lookup = exec.LookPath
	}
	executable, err := lookup("why-diff")
	if err != nil {
		report.Checks = append(report.Checks, Check{
			Name:   "Hook executable",
			Status: StatusError,
			Detail: "`why-diff` is not on PATH; configured agents cannot run the generated hook command",
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
	hookExecutable, hookErr := lookup("why-diff-hook")
	if hookErr != nil {
		report.Checks = append(report.Checks, Check{
			Name: "Hook recorder", Status: StatusError,
			Detail: "`why-diff-hook` is not on PATH; install or rebuild the lightweight recorder",
		})
	} else {
		report.Checks = append(report.Checks, Check{
			Name: "Hook recorder", Status: StatusOK, Detail: hookExecutable,
		})
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
			warningDetail = fmt.Sprintf("%d warning(s); inspect affected sessions with `why-diff show`", warningCount)
		}
		report.Checks = append(report.Checks, Check{Name: "Capture quality", Status: warningStatus, Detail: warningDetail})
	}

	indexPath := indexdb.Path(dataRoot)
	index, indexErr := indexdb.Open(indexPath)
	if indexErr == nil {
		stats, statsErr := index.Stats(ctx)
		_ = index.Close()
		if statsErr == nil {
			report.Checks = append(report.Checks, Check{
				Name: "SQLite index", Status: StatusOK,
				Detail: fmt.Sprintf("%d events, %d changes, %d entities, %d lineage edges", stats.Events, stats.Changes, stats.Entities, stats.Edges),
			})
		} else {
			report.Checks = append(report.Checks, Check{
				Name: "SQLite index", Status: StatusWarning,
				Detail: "unreadable projection; the next query will rebuild it from canonical provenance",
			})
		}
	} else {
		report.Checks = append(report.Checks, Check{
			Name: "SQLite index", Status: StatusWarning,
			Detail: "not built yet; the first query will build it from canonical provenance",
		})
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
