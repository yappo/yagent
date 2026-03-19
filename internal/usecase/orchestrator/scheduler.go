package orchestrator

import (
	"path/filepath"
	"strings"

	"yagent/internal/domain"
)

type scheduleSpec struct {
	ID              string
	DependsOn       []string
	ReadSet         []string
	WriteSet        []string
	SideEffectClass domain.SideEffectClass
	DuplicateKey    string
	Source          string
	SourceLimit     int
}

type runtimeScheduler struct {
	maxParallel int
}

func newRuntimeScheduler(maxParallel int) *runtimeScheduler {
	if maxParallel < 1 {
		maxParallel = 1
	}
	return &runtimeScheduler{maxParallel: maxParallel}
}

func (s *runtimeScheduler) nextBatch(items []scheduleSpec, completed map[string]bool) []int {
	ready := make([]int, 0, len(items))
	for idx, item := range items {
		if completed[item.ID] {
			continue
		}
		if !depsSatisfied(item.DependsOn, completed) {
			continue
		}
		ready = append(ready, idx)
	}

	batch := make([]int, 0, minInt(len(ready), s.maxParallel))
	sourceUsage := map[string]int{}
	for _, idx := range ready {
		if len(batch) >= s.maxParallel {
			break
		}
		if conflictsWithBatch(items[idx], items, batch, sourceUsage) {
			continue
		}
		batch = append(batch, idx)
		source := items[idx].Source
		if source != "" {
			sourceUsage[source]++
		}
	}
	return batch
}

func depsSatisfied(dependsOn []string, completed map[string]bool) bool {
	for _, dep := range dependsOn {
		if !completed[dep] {
			return false
		}
	}
	return true
}

func conflictsWithBatch(candidate scheduleSpec, items []scheduleSpec, batch []int, sourceUsage map[string]int) bool {
	if candidate.Source != "" {
		limit := candidate.SourceLimit
		if limit <= 0 {
			limit = defaultSourceLimit(candidate.Source)
		}
		if sourceUsage[candidate.Source] >= limit {
			return true
		}
	}
	for _, idx := range batch {
		current := items[idx]
		if candidate.DuplicateKey != "" && candidate.DuplicateKey == current.DuplicateKey {
			return true
		}
		if accessConflict(candidate.ReadSet, candidate.WriteSet, current.ReadSet, current.WriteSet) {
			return true
		}
		if serialSideEffect(candidate.SideEffectClass) || serialSideEffect(current.SideEffectClass) {
			return true
		}
	}
	return false
}

func defaultSourceLimit(source string) int {
	switch source {
	case "task", "patch", "agent":
		return 1
	default:
		return 4
	}
}

func serialSideEffect(class domain.SideEffectClass) bool {
	switch class {
	case domain.SideEffectWorkspace, domain.SideEffectProcess, domain.SideEffectExternal:
		return true
	default:
		return false
	}
}

func accessConflict(readLeft []string, writeLeft []string, readRight []string, writeRight []string) bool {
	return pathsIntersect(writeLeft, readRight) || pathsIntersect(writeLeft, writeRight) || pathsIntersect(writeRight, readLeft)
}

func pathsIntersect(left []string, right []string) bool {
	if len(left) == 0 || len(right) == 0 {
		return false
	}
	for _, l := range left {
		for _, r := range right {
			if sameOrNestedPath(l, r) || sameOrNestedPath(r, l) {
				return true
			}
		}
	}
	return false
}

func sameOrNestedPath(base string, candidate string) bool {
	if base == candidate {
		return true
	}
	base = strings.TrimSuffix(base, string(filepath.Separator))
	candidate = strings.TrimSuffix(candidate, string(filepath.Separator))
	if base == "" || candidate == "" {
		return false
	}
	return strings.HasPrefix(candidate, base+string(filepath.Separator))
}

func minInt(left int, right int) int {
	if left < right {
		return left
	}
	return right
}
