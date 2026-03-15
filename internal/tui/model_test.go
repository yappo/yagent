package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"yagent/internal/domain"
	chatusecase "yagent/internal/usecase/chat"
)

func newTestModel(t *testing.T) model {
	t.Helper()
	return newModel(chatusecase.NewService(nil, nil, 1), t.TempDir())
}

func TestPermissionRequestState(t *testing.T) {
	m := newTestModel(t)
	response := make(chan domain.PermissionDecision, 1)

	modelValue, _ := m.Update(permissionRequestMsg{
		request: domain.PermissionRequest{
			ToolName:  "file_reader",
			Operation: "ファイル読み取り",
			Resource:  "/tmp/a.txt",
		},
		response: response,
	})

	next := modelValue.(model)
	if next.permission == nil {
		t.Fatalf("permission state was not set")
	}
}

func TestViewportScrollKey(t *testing.T) {
	m := newTestModel(t)
	m.width = 80
	m.height = 24
	m.output = appendOutputBlock(nil, assistantOutputLabel, strings.Repeat("a\n", 20))
	m.syncLayout()

	modelValue, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
	next := modelValue.(model)
	if next.viewport.YOffset() < 0 {
		t.Fatalf("viewport offset should not be negative")
	}
}

func TestRenderLogWrapsLongLines(t *testing.T) {
	m := newTestModel(t)
	m.viewport.SetWidth(10)
	m.output = appendOutputBlock(nil, assistantOutputLabel, "aaaaaaaaaaaa")

	rendered := m.renderLog()
	if !strings.Contains(rendered, "aaaaaaaaaa\naa") {
		t.Fatalf("expected wrapped content, got %q", rendered)
	}
}

func TestCommandCandidatesForSlash(t *testing.T) {
	m := newTestModel(t)
	m.textarea.SetValue("/")

	candidates := m.activeSlashCompletion().candidates
	if len(candidates) != 3 {
		t.Fatalf("expected 3 candidates, got %d", len(candidates))
	}
}

func TestCommandCandidatesFiltered(t *testing.T) {
	m := newTestModel(t)
	m.textarea.SetValue("/cl")

	candidates := m.activeSlashCompletion().candidates
	if len(candidates) != 1 || candidates[0].value != "/clear" {
		t.Fatalf("unexpected candidates: %+v", candidates)
	}
}

func TestTabCompletesFirstCommandCandidate(t *testing.T) {
	m := newTestModel(t)
	m.textarea.SetValue("/he")

	modelValue, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	next := modelValue.(model)
	if next.textarea.Value() != "/help" {
		t.Fatalf("expected /help, got %q", next.textarea.Value())
	}
}

func TestTabDoesNothingWithoutCandidate(t *testing.T) {
	m := newTestModel(t)
	m.textarea.SetValue("/x")

	modelValue, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	next := modelValue.(model)
	if next.textarea.Value() != "/x" {
		t.Fatalf("textarea value changed unexpectedly: %q", next.textarea.Value())
	}
}

func TestPermissionTabDoesNotTriggerCommandCompletion(t *testing.T) {
	m := newTestModel(t)
	m.textarea.SetValue("/he")
	response := make(chan domain.PermissionDecision, 1)
	m.permission = &permissionState{
		request: domain.PermissionRequest{
			ToolName:  "file_reader",
			Operation: "ファイル読み取り",
			Resource:  "/tmp/a.txt",
		},
		response:      response,
		selectedIndex: 0,
	}

	modelValue, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	next := modelValue.(model)
	if next.textarea.Value() != "/he" {
		t.Fatalf("permission tab should not complete command: %q", next.textarea.Value())
	}
}

func TestViewShowsCommandCandidates(t *testing.T) {
	m := newTestModel(t)
	m.width = 80
	m.height = 24
	m.textarea.SetValue("/he")
	m.syncLayout()

	view := m.View().Content
	if !strings.Contains(view, "候補: Tab で補完") {
		t.Fatalf("expected command hint in view, got %q", view)
	}
	if !strings.Contains(view, "/help") {
		t.Fatalf("expected /help candidate in view, got %q", view)
	}
}

func TestUpInMultilineComposerMovesCursorBeforeHistory(t *testing.T) {
	m := newTestModel(t)
	m.history = []string{"previous prompt"}
	m.historyIndex = len(m.history)
	m.textarea.SetValue("first line\nsecond line")

	modelValue, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	next := modelValue.(model)

	if next.textarea.Value() != "first line\nsecond line" {
		t.Fatalf("textarea value should stay in draft, got %q", next.textarea.Value())
	}
	if next.textarea.Line() != 0 {
		t.Fatalf("cursor should move to first line, got line %d", next.textarea.Line())
	}
}

func TestUpAtFirstLineFallsBackToHistory(t *testing.T) {
	m := newTestModel(t)
	m.history = []string{"previous prompt"}
	m.historyIndex = len(m.history)
	m.textarea.SetValue("first line\nsecond line")
	m.textarea.CursorUp()

	modelValue, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	next := modelValue.(model)

	if next.textarea.Value() != "previous prompt" {
		t.Fatalf("expected history prompt, got %q", next.textarea.Value())
	}
}

func TestDownInMultilineComposerMovesCursorBeforeHistory(t *testing.T) {
	m := newTestModel(t)
	m.history = []string{"previous prompt"}
	m.historyIndex = 0
	m.textarea.SetValue("first line\nsecond line")
	m.textarea.CursorUp()

	modelValue, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	next := modelValue.(model)

	if next.textarea.Value() != "first line\nsecond line" {
		t.Fatalf("textarea value should stay in draft, got %q", next.textarea.Value())
	}
	if next.textarea.Line() != 1 {
		t.Fatalf("cursor should move to last line, got line %d", next.textarea.Line())
	}
}

func TestDownAtLastLineFallsBackToHistoryBehavior(t *testing.T) {
	m := newTestModel(t)
	m.history = []string{"previous prompt"}
	m.historyIndex = 0
	m.textarea.SetValue("first line\nsecond line")

	modelValue, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	next := modelValue.(model)

	if next.textarea.Value() != "" {
		t.Fatalf("expected composer reset after leaving history, got %q", next.textarea.Value())
	}
	if next.historyIndex != len(next.history) {
		t.Fatalf("expected history index at end, got %d", next.historyIndex)
	}
}

func TestPathCandidatesForRelativeFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	m := newModel(chatusecase.NewService(nil, nil, 1), dir)
	m.textarea.SetValue("@R")

	ctx := m.activePathCompletion()
	if ctx == nil || len(ctx.candidates) != 1 || ctx.candidates[0].display != "README.md" {
		t.Fatalf("unexpected path candidates: %+v", ctx)
	}
}

func TestPathCandidatesForCurrentDirectoryPrefix(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	m := newModel(chatusecase.NewService(nil, nil, 1), dir)
	m.textarea.SetValue("@./")

	ctx := m.activePathCompletion()
	if ctx == nil || len(ctx.candidates) != 1 || ctx.candidates[0].display != "./README.md" {
		t.Fatalf("unexpected current directory candidates: %+v", ctx)
	}
}

func TestPathCandidatesIncludeDirectoriesAndFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "lib"), 0o755); err != nil {
		t.Fatalf("mkdir lib: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "libexe"), 0o755); err != nil {
		t.Fatalf("mkdir libexe: %v", err)
	}
	m := newModel(chatusecase.NewService(nil, nil, 1), dir)
	m.textarea.SetValue("@l")

	ctx := m.activePathCompletion()
	if ctx == nil || len(ctx.candidates) != 2 {
		t.Fatalf("unexpected candidates: %+v", ctx)
	}
	if ctx.candidates[0].display != "lib/" || ctx.candidates[1].display != "libexe/" {
		t.Fatalf("unexpected candidate order: %+v", ctx.candidates)
	}
}

func TestPathCandidatesForDirectoryContents(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "lib"), 0o755); err != nil {
		t.Fatalf("mkdir lib: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "lib", "file.go"), []byte("package lib"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	m := newModel(chatusecase.NewService(nil, nil, 1), dir)
	m.textarea.SetValue("@lib/")

	ctx := m.activePathCompletion()
	if ctx == nil || len(ctx.candidates) != 1 || ctx.candidates[0].display != "lib/file.go" {
		t.Fatalf("unexpected nested candidates: %+v", ctx)
	}
}

func TestPathCandidatesIncludeHiddenEntries(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("A=B"), 0o644); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	m := newModel(chatusecase.NewService(nil, nil, 1), dir)
	m.textarea.SetValue("@.")

	ctx := m.activePathCompletion()
	if ctx == nil {
		t.Fatalf("expected hidden candidates")
	}
	displays := []string{ctx.candidates[0].display, ctx.candidates[1].display}
	if !strings.Contains(strings.Join(displays, ","), ".git/") || !strings.Contains(strings.Join(displays, ","), ".env") {
		t.Fatalf("hidden entries not found: %+v", ctx.candidates)
	}
}

func TestPathCompletionTabCompletesSingleFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	m := newModel(chatusecase.NewService(nil, nil, 1), dir)
	m.textarea.SetValue("@R")

	modelValue, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	next := modelValue.(model)
	if next.textarea.Value() != "@README.md" {
		t.Fatalf("expected @README.md, got %q", next.textarea.Value())
	}
}

func TestPathCompletionTabCompletesDirectoryWithSlash(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "lib"), 0o755); err != nil {
		t.Fatalf("mkdir lib: %v", err)
	}
	m := newModel(chatusecase.NewService(nil, nil, 1), dir)
	m.textarea.SetValue("@l")

	modelValue, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	next := modelValue.(model)
	if next.textarea.Value() != "@lib/" {
		t.Fatalf("expected @lib/, got %q", next.textarea.Value())
	}
}

func TestPathCompletionTabUsesLongestCommonPrefix(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "lib"), 0o755); err != nil {
		t.Fatalf("mkdir lib: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "libexe"), 0o755); err != nil {
		t.Fatalf("mkdir libexe: %v", err)
	}
	m := newModel(chatusecase.NewService(nil, nil, 1), dir)
	m.textarea.SetValue("@l")

	modelValue, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	next := modelValue.(model)
	if next.textarea.Value() != "@lib" {
		t.Fatalf("expected @lib, got %q", next.textarea.Value())
	}
}

func TestNormalizePromptReferencesUsesOnlySelectedRefs(t *testing.T) {
	got := normalizePromptReferences("@main.go の概要を見せて", map[string]string{
		"@main.go": "main.go",
	})
	if got != "main.go の概要を見せて" {
		t.Fatalf("unexpected normalized prompt: %q", got)
	}
}

func TestNormalizePromptReferencesKeepsUnselectedToken(t *testing.T) {
	got := normalizePromptReferences("@missing.go の概要を見せて", map[string]string{
		"@main.go": "main.go",
	})
	if got != "@missing.go の概要を見せて" {
		t.Fatalf("unexpected normalized prompt: %q", got)
	}
}

func TestSubmitPromptStoresNormalizedMessageOnlyForSelectedReference(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}

	m := newModel(chatusecase.NewService(nil, nil, 1), dir)
	m.selectedRefs["@main.go"] = "main.go"
	modelValue, _ := submitPrompt(m, "@main.go の概要を見せて")
	next := modelValue.(model)

	if len(next.messages) != 1 || next.messages[0].Content != "main.go の概要を見せて" {
		t.Fatalf("unexpected stored message: %+v", next.messages)
	}
	if !strings.Contains(strings.Join(next.output, "\n"), "@main.go の概要を見せて") {
		t.Fatalf("original prompt was not kept in output: %+v", next.output)
	}
}

func TestSubmitPromptKeepsManualAtReferenceUnchanged(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}

	m := newModel(chatusecase.NewService(nil, nil, 1), dir)
	modelValue, _ := submitPrompt(m, "@main.go の概要を見せて")
	next := modelValue.(model)

	if len(next.messages) != 1 || next.messages[0].Content != "@main.go の概要を見せて" {
		t.Fatalf("unexpected stored message: %+v", next.messages)
	}
}
