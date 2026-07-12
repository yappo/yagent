package orchestrator

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"yagent/internal/domain"
)

var repositoryPathClaimPattern = regexp.MustCompile(`(?:^|[^A-Za-z0-9_.-])((?:internal|cmd|pkg|docs|testdata|\.yagent)/[A-Za-z0-9_./-]+)`)

func ungroundedRepositoryPaths(run *domain.RunState, response string) []string {
	known := groundedRepositoryPaths(run)
	claims := repositoryPathClaimPattern.FindAllStringSubmatch(response, -1)
	unknown := map[string]struct{}{}
	for _, match := range claims {
		if len(match) < 2 {
			continue
		}
		claim := normalizeGroundingPath(match[1])
		if claim == "" || groundedPathKnown(claim, known) {
			continue
		}
		unknown[claim] = struct{}{}
	}
	out := make([]string, 0, len(unknown))
	for path := range unknown {
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}

func groundedRepositoryPaths(run *domain.RunState) []string {
	if run == nil {
		return nil
	}
	paths := []string{}
	for _, artifact := range run.Artifacts {
		switch artifact.Kind {
		case "repo_map":
			var payload domain.RepoMapArtifactPayload
			if json.Unmarshal(artifact.Payload, &payload) != nil {
				continue
			}
			for _, entry := range payload.Entries {
				if entry.Source == "messages" {
					continue
				}
				paths = append(paths, normalizeGroundingPath(entry.Path))
			}
		case "change_set":
			var payload domain.ChangeSetArtifactPayload
			if json.Unmarshal(artifact.Payload, &payload) != nil {
				continue
			}
			for _, file := range payload.Files {
				paths = append(paths, normalizeGroundingPath(file.Path))
			}
		}
	}
	return compactPaths(paths)
}

func groundedPathKnown(claim string, known []string) bool {
	for _, path := range known {
		if path == claim || strings.HasPrefix(path, claim+"/") || strings.HasPrefix(path, claim+".") {
			return true
		}
	}
	return false
}

func normalizeGroundingPath(value string) string {
	value = strings.Trim(value, "`'\"()[]{}<>,.;:!?")
	value = strings.TrimPrefix(value, "./")
	if value == "" {
		return ""
	}
	return filepath.ToSlash(filepath.Clean(value))
}
