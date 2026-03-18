package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

const (
	pathCompletionDebounceInterval = 120 * time.Millisecond
	pathCompletionCacheTTL         = time.Second
	maxPathCompletionCacheDirs     = 16
)

var readDirEntries = os.ReadDir

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

type completionState struct {
	ctx          *completionContext
	rendered     string
	height       int
	pendingSeq   int
	nextSeq      int
	pendingToken string
	pendingDir   string
	pathDirs     map[string]pathDirSnapshot
}

type pathCompletionDebounceMsg struct {
	seq int
}

type pathCompletionQuery struct {
	start        int
	end          int
	token        string
	rawDirPart   string
	fragment     string
	absSearchDir string
}

type pathDirSnapshot struct {
	loadedAt time.Time
	entries  []pathDirEntry
}

type pathDirEntry struct {
	name  string
	isDir bool
}

func pathCompletionDebounceCmd(seq int) tea.Cmd {
	return tea.Tick(pathCompletionDebounceInterval, func(time.Time) tea.Msg {
		return pathCompletionDebounceMsg{seq: seq}
	})
}

func batchCmds(cmds ...tea.Cmd) tea.Cmd {
	filtered := make([]tea.Cmd, 0, len(cmds))
	for _, cmd := range cmds {
		if cmd != nil {
			filtered = append(filtered, cmd)
		}
	}
	switch len(filtered) {
	case 0:
		return nil
	case 1:
		return filtered[0]
	default:
		return tea.Batch(filtered...)
	}
}

func (m *model) syncAfterComposerChange(immediatePath bool) tea.Cmd {
	m.composerCache.dirty = true
	cmd := m.refreshCompletionState(immediatePath)
	m.syncLayout()
	return cmd
}

func (m *model) resetComposerAndSync() tea.Cmd {
	m.textarea.Reset()
	m.reconcileSelectedRefs()
	return m.syncAfterComposerChange(false)
}

func (m model) hasCompletionCandidates() bool {
	return m.completion.ctx != nil && len(m.completion.ctx.candidates) > 0
}

func (m *model) applyCompletion() tea.Cmd {
	_ = m.refreshCompletionState(true)
	ctx := m.completion.ctx
	if ctx == nil || len(ctx.candidates) == 0 {
		return nil
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
		return nil
	}

	m.textarea.SetValue(replaceRuneRange(m.textarea.Value(), ctx.start, ctx.end, replacement))
	if ctx.kind == completionKindPath && normalized != "" {
		m.selectedRefs[replacement] = normalized
	}
	m.reconcileSelectedRefs()
	return m.syncAfterComposerChange(true)
}

func (m model) renderCompletionSuggestions() string {
	return m.completion.rendered
}

func (m model) completionSuggestionsHeight() int {
	return m.completion.height
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
	query := m.currentPathCompletionQuery()
	if query == nil {
		return nil
	}

	candidates := resolvePathCandidatesAt(query.rawDirPart, query.fragment, query.absSearchDir)
	if len(candidates) == 0 {
		return nil
	}

	return &completionContext{
		kind:       completionKindPath,
		start:      query.start,
		end:        query.end,
		token:      query.token,
		candidates: candidates,
	}
}

func (m *model) refreshCompletionState(immediatePath bool) tea.Cmd {
	if ctx := m.activeSlashCompletion(); ctx != nil {
		m.cancelPendingPathCompletion()
		m.setCompletionContext(ctx)
		return nil
	}

	if !strings.Contains(m.textarea.Value(), "@") {
		m.cancelPendingPathCompletion()
		m.setCompletionContext(nil)
		return nil
	}

	query := m.currentPathCompletionQuery()
	if query == nil {
		m.cancelPendingPathCompletion()
		m.setCompletionContext(nil)
		return nil
	}

	if ctx, resolved := m.resolvePathCompletionQuery(query, immediatePath); resolved {
		m.cancelPendingPathCompletion()
		m.setCompletionContext(ctx)
		return nil
	}

	m.setCompletionContext(nil)
	if m.completion.pendingSeq != 0 && m.completion.pendingToken == query.token && m.completion.pendingDir == query.absSearchDir {
		return nil
	}

	m.completion.nextSeq++
	m.completion.pendingSeq = m.completion.nextSeq
	m.completion.pendingToken = query.token
	m.completion.pendingDir = query.absSearchDir
	return pathCompletionDebounceCmd(m.completion.pendingSeq)
}

func (m *model) cancelPendingPathCompletion() {
	m.completion.pendingSeq = 0
	m.completion.pendingToken = ""
	m.completion.pendingDir = ""
}

func (m *model) setCompletionContext(ctx *completionContext) {
	m.completion.ctx = ctx
	if ctx == nil || len(ctx.candidates) == 0 {
		m.completion.rendered = ""
		m.completion.height = 0
		m.completionCache.dirty = false
		return
	}

	m.completion.rendered = renderCompletionSuggestionsForContext(m.styles, ctx)
	m.completion.height = lipgloss.Height(m.completion.rendered)
	m.completionCache.dirty = false
}

func renderCompletionSuggestionsForContext(styles styles, ctx *completionContext) string {
	if ctx == nil || len(ctx.candidates) == 0 {
		return ""
	}

	hint := "候補: Tab で補完"
	if ctx.kind == completionKindPath {
		hint = "パス候補: Tab で補完"
	}

	lines := make([]string, 0, len(ctx.candidates)+1)
	lines = append(lines, styles.commandHint.Render(hint))
	for index, candidate := range ctx.candidates {
		label := candidate.display
		if candidate.description != "" {
			label = fmt.Sprintf("%s  %s", label, candidate.description)
		}
		if index == 0 {
			lines = append(lines, styles.commandSelected.Render(label))
			continue
		}
		lines = append(lines, styles.commandCandidate.Render(label))
	}
	return strings.Join(lines, "\n")
}

func (m *model) resolvePathCompletionQuery(query *pathCompletionQuery, allowRead bool) (*completionContext, bool) {
	snapshot, resolved := m.pathDirectorySnapshot(query.absSearchDir, allowRead)
	if !resolved {
		return nil, false
	}

	candidates := resolvePathCandidatesFromSnapshot(query.rawDirPart, query.fragment, snapshot.entries)
	if len(candidates) == 0 {
		return nil, true
	}

	return &completionContext{
		kind:       completionKindPath,
		start:      query.start,
		end:        query.end,
		token:      query.token,
		candidates: candidates,
	}, true
}

func (m *model) pathDirectorySnapshot(absSearchDir string, allowRead bool) (pathDirSnapshot, bool) {
	if snapshot, ok := m.completion.pathDirs[absSearchDir]; ok && time.Since(snapshot.loadedAt) <= pathCompletionCacheTTL {
		return snapshot, true
	}
	if !allowRead {
		return pathDirSnapshot{}, false
	}

	entries, err := readDirEntries(absSearchDir)
	if err != nil {
		delete(m.completion.pathDirs, absSearchDir)
		return pathDirSnapshot{}, true
	}

	snapshot := pathDirSnapshot{
		loadedAt: time.Now(),
		entries:  snapshotPathEntries(entries),
	}
	m.completion.pathDirs[absSearchDir] = snapshot
	m.prunePathDirectorySnapshots()
	return snapshot, true
}

func (m *model) prunePathDirectorySnapshots() {
	if len(m.completion.pathDirs) <= maxPathCompletionCacheDirs {
		return
	}

	type snapshotMeta struct {
		dir      string
		loadedAt time.Time
	}
	metas := make([]snapshotMeta, 0, len(m.completion.pathDirs))
	for dir, snapshot := range m.completion.pathDirs {
		metas = append(metas, snapshotMeta{dir: dir, loadedAt: snapshot.loadedAt})
	}
	sort.Slice(metas, func(i, j int) bool {
		return metas[i].loadedAt.Before(metas[j].loadedAt)
	})
	for len(metas) > maxPathCompletionCacheDirs {
		delete(m.completion.pathDirs, metas[0].dir)
		metas = metas[1:]
	}
}

func snapshotPathEntries(entries []os.DirEntry) []pathDirEntry {
	snapshots := make([]pathDirEntry, 0, len(entries))
	for _, entry := range entries {
		snapshots = append(snapshots, pathDirEntry{
			name:  entry.Name(),
			isDir: entry.IsDir(),
		})
	}
	return snapshots
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

	return resolvePathCandidatesAt(rawDirPart, fragment, absSearchDir)
}

func resolvePathCandidatesAt(rawDirPart, fragment, absSearchDir string) []completionCandidate {
	entries, err := readDirEntries(absSearchDir)
	if err != nil {
		return nil
	}
	return resolvePathCandidatesFromSnapshot(rawDirPart, fragment, snapshotPathEntries(entries))
}

func resolvePathCandidatesFromSnapshot(rawDirPart, fragment string, entries []pathDirEntry) []completionCandidate {
	filtered := make([]completionCandidate, 0, len(entries))
	for _, entry := range entries {
		if !strings.HasPrefix(strings.ToLower(entry.name), strings.ToLower(fragment)) {
			continue
		}

		replacement := rawDirPart + entry.name
		display := replacement
		if entry.isDir {
			replacement += "/"
			display += "/"
		}

		filtered = append(filtered, completionCandidate{
			value:      "@" + filepath.ToSlash(replacement),
			normalized: filepath.ToSlash(replacement),
			display:    filepath.ToSlash(display),
			isDir:      entry.isDir,
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

func (m model) currentPathCompletionQuery() *pathCompletionQuery {
	if m.workingDir == "" {
		return nil
	}
	start, end, token, ok := m.currentTokenBounds()
	if !ok || !strings.HasPrefix(token, "@") {
		return nil
	}
	if strings.HasPrefix(token, "@/") || strings.Contains(token, `\`) {
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

	absSearchDir := filepath.Clean(filepath.Join(m.workingDir, filepath.FromSlash(searchDir)))
	if !isWithinRoot(m.workingDir, absSearchDir) {
		return nil
	}

	return &pathCompletionQuery{
		start:        start,
		end:          end,
		token:        token,
		rawDirPart:   rawDirPart,
		fragment:     fragment,
		absSearchDir: absSearchDir,
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
