package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"charm.land/lipgloss/v2"
)

type completionKind string

const (
	completionKindSlash completionKind = "slash"
	completionKindPath  completionKind = "path"
)

type completionCandidate struct {
	value       string
	normalized  string
	display     string
	description string
	isDir       bool
}

type completionContext struct {
	kind       completionKind
	start      int
	end        int
	token      string
	candidates []completionCandidate
}

func (m model) hasCompletionCandidates() bool {
	ctx := m.activeCompletion()
	return ctx != nil && len(ctx.candidates) > 0
}

func (m *model) applyCompletion() {
	ctx := m.activeCompletion()
	if ctx == nil || len(ctx.candidates) == 0 {
		return
	}

	replacement := ctx.token
	normalized := ""
	switch len(ctx.candidates) {
	case 1:
		replacement = ctx.candidates[0].value
		normalized = ctx.candidates[0].normalized
	default:
		values := make([]string, 0, len(ctx.candidates))
		for _, candidate := range ctx.candidates {
			values = append(values, candidate.value)
		}
		if commonPrefix := longestCommonPrefix(values); len(commonPrefix) > len(ctx.token) {
			replacement = commonPrefix
		}
	}

	if replacement == ctx.token {
		return
	}

	m.textarea.SetValue(replaceRuneRange(m.textarea.Value(), ctx.start, ctx.end, replacement))
	if ctx.kind == completionKindPath && normalized != "" {
		m.selectedRefs[replacement] = normalized
	}
	m.reconcileSelectedRefs()
	m.syncLayout()
}

func (m model) renderCompletionSuggestions() string {
	ctx := m.activeCompletion()
	if ctx == nil || len(ctx.candidates) == 0 {
		return ""
	}

	hint := "候補: Tab で補完"
	if ctx.kind == completionKindPath {
		hint = "パス候補: Tab で補完"
	}

	lines := make([]string, 0, len(ctx.candidates)+1)
	lines = append(lines, m.styles.commandHint.Render(hint))
	for index, candidate := range ctx.candidates {
		label := candidate.display
		if candidate.description != "" {
			label = fmt.Sprintf("%s  %s", label, candidate.description)
		}
		if index == 0 {
			lines = append(lines, m.styles.commandSelected.Render(label))
			continue
		}
		lines = append(lines, m.styles.commandCandidate.Render(label))
	}

	return strings.Join(lines, "\n")
}

func (m model) completionSuggestionsHeight() int {
	suggestions := m.renderCompletionSuggestions()
	if suggestions == "" {
		return 0
	}
	return lipgloss.Height(suggestions)
}

func (m model) activeCompletion() *completionContext {
	if ctx := m.activePathCompletion(); ctx != nil {
		return ctx
	}
	return m.activeSlashCompletion()
}

func (m model) activeSlashCompletion() *completionContext {
	input := m.currentInput()
	if !strings.HasPrefix(input, "/") || strings.ContainsAny(input, " \t\n") {
		return nil
	}

	candidates := make([]completionCandidate, 0, len(slashCommands))
	for _, command := range slashCommands {
		if strings.HasPrefix(command.name, input) {
			candidates = append(candidates, completionCandidate{
				value:       command.name,
				display:     command.name,
				description: command.description,
			})
		}
	}
	if len(candidates) == 0 {
		return nil
	}

	return &completionContext{
		kind:       completionKindSlash,
		start:      0,
		end:        len([]rune(m.textarea.Value())),
		token:      input,
		candidates: candidates,
	}
}

func (m model) activePathCompletion() *completionContext {
	start, end, token, ok := m.currentTokenBounds()
	if !ok || !strings.HasPrefix(token, "@") {
		return nil
	}
	if strings.HasPrefix(token, "@/") || strings.Contains(token, `\`) {
		return nil
	}

	candidates := resolvePathCandidates(m.workingDir, token)
	if len(candidates) == 0 {
		return nil
	}

	return &completionContext{
		kind:       completionKindPath,
		start:      start,
		end:        end,
		token:      token,
		candidates: candidates,
	}
}

func (m model) currentTokenBounds() (int, int, string, bool) {
	value := []rune(m.textarea.Value())
	if len(value) == 0 {
		return 0, 0, "", false
	}

	cursor := m.cursorRuneIndex()
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(value) {
		cursor = len(value)
	}

	start := cursor
	for start > 0 && !unicode.IsSpace(value[start-1]) {
		start--
	}

	end := cursor
	for end < len(value) && !unicode.IsSpace(value[end]) {
		end++
	}

	if start == end {
		return 0, 0, "", false
	}

	return start, end, string(value[start:end]), true
}

func (m model) cursorRuneIndex() int {
	lines := strings.Split(m.textarea.Value(), "\n")
	line := m.textarea.Line()
	if line < 0 {
		return 0
	}
	if line >= len(lines) {
		line = len(lines) - 1
	}

	index := 0
	for i := 0; i < line; i++ {
		index += len([]rune(lines[i])) + 1
	}
	return index + m.textarea.LineInfo().CharOffset
}

func resolvePathCandidates(workingDir, token string) []completionCandidate {
	if workingDir == "" {
		return nil
	}

	pathExpr := strings.TrimPrefix(token, "@")
	if strings.Contains(pathExpr, "..") {
		return nil
	}

	rawDirPart, fragment := splitPathExpression(pathExpr)
	searchDir := "."
	if rawDirPart != "" {
		searchDir = strings.TrimSuffix(rawDirPart, "/")
	}

	absSearchDir := filepath.Clean(filepath.Join(workingDir, filepath.FromSlash(searchDir)))
	if !isWithinRoot(workingDir, absSearchDir) {
		return nil
	}

	entries, err := os.ReadDir(absSearchDir)
	if err != nil {
		return nil
	}

	filtered := make([]completionCandidate, 0, len(entries))
	for _, entry := range entries {
		if !strings.HasPrefix(strings.ToLower(entry.Name()), strings.ToLower(fragment)) {
			continue
		}

		replacement := rawDirPart + entry.Name()
		display := replacement
		if entry.IsDir() {
			replacement += "/"
			display += "/"
		}

		filtered = append(filtered, completionCandidate{
			value:      "@" + filepath.ToSlash(replacement),
			normalized: filepath.ToSlash(replacement),
			display:    filepath.ToSlash(display),
			isDir:      entry.IsDir(),
		})
	}

	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].isDir != filtered[j].isDir {
			return filtered[i].isDir
		}
		return strings.ToLower(filtered[i].display) < strings.ToLower(filtered[j].display)
	})

	return filtered
}

func splitPathExpression(expr string) (string, string) {
	index := strings.LastIndex(expr, "/")
	if index < 0 {
		return "", expr
	}
	return expr[:index+1], expr[index+1:]
}

func isWithinRoot(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != "..")
}

func replaceRuneRange(input string, start, end int, replacement string) string {
	runes := []rune(input)
	if start < 0 {
		start = 0
	}
	if end > len(runes) {
		end = len(runes)
	}
	if start > end {
		start = end
	}
	return string(runes[:start]) + replacement + string(runes[end:])
}

func longestCommonPrefix(values []string) string {
	if len(values) == 0 {
		return ""
	}

	prefix := values[0]
	for _, value := range values[1:] {
		for !strings.HasPrefix(value, prefix) && prefix != "" {
			prefix = prefix[:len(prefix)-1]
		}
		if prefix == "" {
			return ""
		}
	}
	return prefix
}
