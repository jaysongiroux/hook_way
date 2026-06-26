package queue

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
)

// ConflictPolicy controls how a publisher reacts when an existing AWS resource's
// attributes diverge from the configured ones.
type ConflictPolicy string

const (
	ConflictError     ConflictPolicy = "error"
	ConflictWarn      ConflictPolicy = "warn"
	ConflictReconcile ConflictPolicy = "reconcile"
)

// toConflictPolicy maps a config string to a ConflictPolicy, defaulting to
// reconcile when empty (config validation guarantees a known value otherwise).
func toConflictPolicy(s string) ConflictPolicy {
	switch ConflictPolicy(s) {
	case ConflictError, ConflictWarn, ConflictReconcile:
		return ConflictPolicy(s)
	default:
		return ConflictReconcile
	}
}

type attrDiff struct {
	name    string
	want    string
	got     string
	mutable bool
}

// diffAttributes compares only the keys present in want (the config-specified
// attributes) against got, classifying each difference via the mutable set. A
// key absent from got compares as "".
func diffAttributes(want, got map[string]string, mutable map[string]bool) []attrDiff {
	names := make([]string, 0, len(want))
	for name := range want {
		names = append(names, name)
	}
	sort.Strings(names)
	var diffs []attrDiff
	for _, name := range names {
		if want[name] == got[name] {
			continue
		}
		diffs = append(diffs, attrDiff{name: name, want: want[name], got: got[name], mutable: mutable[name]})
	}
	return diffs
}

type reconcileParams struct {
	policy   ConflictPolicy
	logger   *slog.Logger
	destID   string
	target   string
	want     map[string]string
	got      map[string]string
	mutable  map[string]bool
	setAttrs func(ctx context.Context, mutableWant map[string]string) error
}

// applyConflictPolicy compares want vs got and acts per policy. Immutable
// differences always error. Returns nil when there is nothing to do.
func applyConflictPolicy(ctx context.Context, p reconcileParams) error {
	diffs := diffAttributes(p.want, p.got, p.mutable)
	if len(diffs) == 0 {
		return nil
	}
	logger := p.logger
	if logger == nil {
		logger = slog.Default()
	}

	// Immutable conflicts can never be reconciled and guarantee broken publishes.
	for _, d := range diffs {
		if !d.mutable {
			logger.Error("immutable resource conflict",
				"destination", p.destID, "target", p.target,
				"attr", d.name, "want", d.want, "got", d.got)
			return fmt.Errorf("destination %q target %q: immutable attribute %s differs (want %q, got %q); cannot reconcile",
				p.destID, p.target, d.name, d.want, d.got)
		}
	}

	switch p.policy {
	case ConflictError:
		for _, d := range diffs {
			logger.Error("resource attribute conflict",
				"destination", p.destID, "target", p.target,
				"attr", d.name, "want", d.want, "got", d.got, "policy", "error")
		}
		return fmt.Errorf("destination %q target %q: attribute %s differs (want %q, got %q); on_existing_conflict=error",
			p.destID, p.target, diffs[0].name, diffs[0].want, diffs[0].got)
	case ConflictWarn:
		for _, d := range diffs {
			logger.Warn("resource drift not reconciled",
				"destination", p.destID, "target", p.target,
				"attr", d.name, "want", d.want, "got", d.got, "policy", "warn")
		}
		return nil
	case ConflictReconcile:
		mutableWant := make(map[string]string, len(diffs))
		for _, d := range diffs {
			mutableWant[d.name] = d.want
		}
		if err := p.setAttrs(ctx, mutableWant); err != nil {
			return fmt.Errorf("destination %q target %q: reconcile failed: %w", p.destID, p.target, err)
		}
		for _, d := range diffs {
			logger.Info("reconciled resource attribute",
				"destination", p.destID, "target", p.target,
				"attr", d.name, "want", d.want, "got", d.got)
		}
		return nil
	default:
		return fmt.Errorf("unknown conflict policy %q", p.policy)
	}
}
