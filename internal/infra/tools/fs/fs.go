package fs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"yagent/internal/domain"
	"yagent/internal/infra/policy"
	"yagent/internal/infra/tools/diffpreview"
	"yagent/internal/infra/tools/execctx"
)

type readTool struct {
	paths    *policy.PathPolicy
	engine   domain.PolicyEngine
	approver domain.Approver
}

type writeTool struct {
	paths    *policy.PathPolicy
	engine   domain.PolicyEngine
	approver domain.Approver
}

type listTool struct {
	paths    *policy.PathPolicy
	engine   domain.PolicyEngine
	approver domain.Approver
}

type statTool struct {
	paths    *policy.PathPolicy
	engine   domain.PolicyEngine
	approver domain.Approver
}

type removeTool struct {
	paths    *policy.PathPolicy
	engine   domain.PolicyEngine
	approver domain.Approver
}

type moveTool struct {
	paths    *policy.PathPolicy
	engine   domain.PolicyEngine
	approver domain.Approver
}

const (
	defaultListDepth        = 0
	defaultListLimitEntries = 80
	maxListLimitEntries     = 500
	maxListScanEntries      = 2000
)

var errListScanLimit = errors.New("fs_list scan limit reached")

func NewReadTool(paths *policy.PathPolicy, engine domain.PolicyEngine, approver domain.Approver) domain.Tool {
	return &readTool{paths: paths, engine: engine, approver: approver}
}

func NewWriteTool(paths *policy.PathPolicy, engine domain.PolicyEngine, approver domain.Approver) domain.Tool {
	return &writeTool{paths: paths, engine: engine, approver: approver}
}

func NewListTool(paths *policy.PathPolicy, engine domain.PolicyEngine, approver domain.Approver) domain.Tool {
	return &listTool{paths: paths, engine: engine, approver: approver}
}

func NewStatTool(paths *policy.PathPolicy, engine domain.PolicyEngine, approver domain.Approver) domain.Tool {
	return &statTool{paths: paths, engine: engine, approver: approver}
}

func NewRemoveTool(paths *policy.PathPolicy, engine domain.PolicyEngine, approver domain.Approver) domain.Tool {
	return &removeTool{paths: paths, engine: engine, approver: approver}
}

func NewMoveTool(paths *policy.PathPolicy, engine domain.PolicyEngine, approver domain.Approver) domain.Tool {
	return &moveTool{paths: paths, engine: engine, approver: approver}
}

func (t *readTool) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:             "fs_read",
		Description:      "指定したテキストファイルを読み取ります。offset と limit_bytes で範囲指定できます。",
		CapabilityGroup:  "fs_read",
		Risk:             "medium",
		RequiresApproval: true,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":        stringSchema("読み取るファイルパス"),
				"offset":      numberSchema("読み取り開始バイト位置"),
				"limit_bytes": numberSchema("最大読み取りバイト数"),
			},
			"required": []string{"path"},
		},
		Metadata: map[string]any{"category": "fs"},
		Semantics: domain.ToolSemantics{
			Class:           domain.ToolClassObserve,
			ReusePolicy:     domain.ToolReuseOnSuccess,
			DuplicatePolicy: domain.ToolDuplicateSuppressInflight,
			Freshness:       domain.ToolFreshnessPolicy{Strategy: domain.ToolFreshnessReadSet},
			SideEffectClass: domain.SideEffectNone,
			Source:          "fs",
			ReadPathArgs:    []string{"path"},
			IdentityArgs:    []string{"path", "offset", "limit_bytes"},
			SourceLimit:     8,
		},
	}
}

func (t *writeTool) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:             "fs_write",
		Description:      "指定したファイルを書き込みます。create と overwrite を明示してください。",
		CapabilityGroup:  "fs_write",
		Risk:             "high",
		RequiresApproval: true,
		MutatesWorkspace: true,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":      stringSchema("書き込むファイルパス"),
				"content":   stringSchema("書き込む内容"),
				"create":    boolSchema("存在しない場合に新規作成する"),
				"overwrite": boolSchema("既存ファイルを上書きする"),
			},
			"required": []string{"path", "content"},
		},
		Metadata: map[string]any{"category": "fs"},
		Semantics: domain.ToolSemantics{
			Class:           domain.ToolClassMutate,
			ReusePolicy:     domain.ToolReuseNever,
			DuplicatePolicy: domain.ToolDuplicateAllow,
			Freshness:       domain.ToolFreshnessPolicy{Strategy: domain.ToolFreshnessNone},
			SideEffectClass: domain.SideEffectWorkspace,
			Source:          "fs",
			WritePathArgs:   []string{"path"},
			IdentityArgs:    []string{"path", "create", "overwrite"},
			SourceLimit:     1,
		},
	}
}

func (t *listTool) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:             "fs_list",
		Description:      "指定ディレクトリ配下のエントリ一覧を summary 付きの bounded JSON で返します。",
		CapabilityGroup:  "fs_read",
		Risk:             "medium",
		RequiresApproval: true,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":           stringSchema("一覧するディレクトリパス"),
				"depth":          numberSchema("再帰の深さ。0 は直下のみ。既定 0"),
				"include_hidden": boolSchema("ドットファイルを含める。既定 false"),
				"limit_entries":  numberSchema(fmt.Sprintf("返却する最大件数。既定 %d、最大 %d", defaultListLimitEntries, maxListLimitEntries)),
			},
			"required": []string{"path"},
		},
		Metadata: map[string]any{"category": "fs"},
		Semantics: domain.ToolSemantics{
			Class:           domain.ToolClassObserve,
			ReusePolicy:     domain.ToolReuseOnSuccess,
			DuplicatePolicy: domain.ToolDuplicateSuppressSemantic,
			Freshness:       domain.ToolFreshnessPolicy{Strategy: domain.ToolFreshnessReadSet},
			SideEffectClass: domain.SideEffectNone,
			Source:          "fs",
			ReadPathArgs:    []string{"path"},
			IdentityArgs:    []string{"path", "depth", "include_hidden", "limit_entries"},
			IdentityDefaults: map[string]any{
				"depth":          defaultListDepth,
				"include_hidden": false,
				"limit_entries":  defaultListLimitEntries,
			},
			SourceLimit: 8,
		},
	}
}

func (t *statTool) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:             "fs_stat",
		Description:      "指定 path のメタデータを返します。",
		CapabilityGroup:  "fs_read",
		Risk:             "low",
		RequiresApproval: true,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": stringSchema("情報を取得する path"),
			},
			"required": []string{"path"},
		},
		Metadata: map[string]any{"category": "fs"},
		Semantics: domain.ToolSemantics{
			Class:           domain.ToolClassObserve,
			ReusePolicy:     domain.ToolReuseOnSuccess,
			DuplicatePolicy: domain.ToolDuplicateSuppressInflight,
			Freshness:       domain.ToolFreshnessPolicy{Strategy: domain.ToolFreshnessReadSet},
			SideEffectClass: domain.SideEffectNone,
			Source:          "fs",
			ReadPathArgs:    []string{"path"},
			IdentityArgs:    []string{"path"},
			SourceLimit:     8,
		},
	}
}

func (t *removeTool) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:             "fs_remove",
		Description:      "ファイルまたはディレクトリを削除します。ディレクトリ削除には recursive=true が必要です。",
		CapabilityGroup:  "fs_write",
		Risk:             "high",
		RequiresApproval: true,
		MutatesWorkspace: true,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":      stringSchema("削除対象 path"),
				"recursive": boolSchema("ディレクトリを再帰削除する"),
				"force":     boolSchema("将来拡張用。v1 では互換のため保持"),
			},
			"required": []string{"path"},
		},
		Metadata: map[string]any{"category": "fs"},
		Semantics: domain.ToolSemantics{
			Class:           domain.ToolClassMutate,
			ReusePolicy:     domain.ToolReuseNever,
			DuplicatePolicy: domain.ToolDuplicateAllow,
			Freshness:       domain.ToolFreshnessPolicy{Strategy: domain.ToolFreshnessNone},
			SideEffectClass: domain.SideEffectWorkspace,
			Source:          "fs",
			WritePathArgs:   []string{"path"},
			IdentityArgs:    []string{"path", "recursive", "force"},
			SourceLimit:     1,
		},
	}
}

func (t *moveTool) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:             "fs_move",
		Description:      "ファイルを移動またはリネームします。",
		CapabilityGroup:  "fs_write",
		Risk:             "high",
		RequiresApproval: true,
		MutatesWorkspace: true,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"source_path":      stringSchema("移動元ファイルパス"),
				"destination_path": stringSchema("移動先ファイルパス"),
			},
			"required": []string{"source_path", "destination_path"},
		},
		Metadata: map[string]any{"category": "fs"},
		Semantics: domain.ToolSemantics{
			Class:           domain.ToolClassMutate,
			ReusePolicy:     domain.ToolReuseNever,
			DuplicatePolicy: domain.ToolDuplicateAllow,
			Freshness:       domain.ToolFreshnessPolicy{Strategy: domain.ToolFreshnessNone},
			SideEffectClass: domain.SideEffectWorkspace,
			Source:          "fs",
			ReadPathArgs:    []string{"source_path"},
			WritePathArgs:   []string{"source_path", "destination_path"},
			IdentityArgs:    []string{"source_path", "destination_path"},
			SourceLimit:     1,
		},
	}
}

func (t *readTool) Execute(ctx context.Context, call domain.ToolCall) domain.ToolResult {
	path, ok := stringArg(call.Arguments, "path")
	if !ok {
		return failure(call, "path パラメータが必要です")
	}
	if err := authorize(ctx, t.engine, t.approver, call); err != nil {
		return failure(call, err.Error())
	}

	resolved, err := t.paths.ResolveFile(path)
	if err != nil {
		return failure(call, err.Error())
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return failure(call, fmt.Sprintf("failed to read file %s: %v", resolved, err))
	}
	if !utf8.Valid(data) || strings.ContainsRune(string(data), '\x00') {
		return failure(call, "バイナリファイルは読み取れません")
	}

	offset := intArg(call.Arguments, "offset", 0)
	limit := intArg(call.Arguments, "limit_bytes", 64*1024)
	if offset < 0 || offset > len(data) {
		return failure(call, "offset が範囲外です")
	}
	if limit <= 0 {
		limit = 64 * 1024
	}
	end := offset + limit
	if end > len(data) {
		end = len(data)
	}

	return success(call, string(data[offset:end]))
}

func (t *writeTool) Execute(ctx context.Context, call domain.ToolCall) domain.ToolResult {
	path, ok := stringArg(call.Arguments, "path")
	if !ok {
		return failure(call, "path パラメータが必要です")
	}
	content, ok := stringArg(call.Arguments, "content")
	if !ok {
		return failure(call, "content パラメータが必要です")
	}

	resolved, err := t.paths.ResolveWritableFile(path)
	if err != nil {
		return failure(call, err.Error())
	}

	var before []byte
	beforeExists := false
	info, statErr := os.Stat(resolved)
	create := boolArg(call.Arguments, "create", false)
	overwrite := boolArg(call.Arguments, "overwrite", false)
	if statErr == nil && !overwrite {
		return failure(call, "既存ファイルを上書きするには overwrite=true が必要です")
	}
	if statErr == nil && info.IsDir() {
		return failure(call, "ディレクトリは fs_write で上書きできません")
	}
	if os.IsNotExist(statErr) && !create {
		return failure(call, "新規ファイルを作成するには create=true が必要です")
	}
	if statErr != nil && !os.IsNotExist(statErr) {
		return failure(call, statErr.Error())
	}
	if statErr == nil {
		beforeExists = true
		before, err = os.ReadFile(resolved)
		if err != nil {
			return failure(call, fmt.Sprintf("failed to read existing file %s: %v", resolved, err))
		}
	}

	if err := authorizeWrite(ctx, t.engine, t.approver, call, resolved, before, beforeExists, content); err != nil {
		return failure(call, err.Error())
	}

	if err := os.WriteFile(resolved, []byte(content), 0o644); err != nil {
		return failure(call, fmt.Sprintf("failed to write file %s: %v", resolved, err))
	}
	return success(call, fmt.Sprintf("ファイル %s に書き込みました", resolved))
}

func (t *listTool) Execute(ctx context.Context, call domain.ToolCall) domain.ToolResult {
	path, ok := stringArg(call.Arguments, "path")
	if !ok {
		return failure(call, "path パラメータが必要です")
	}
	if err := authorize(ctx, t.engine, t.approver, call); err != nil {
		return failure(call, err.Error())
	}

	root, err := t.paths.ResolveDir(path)
	if err != nil {
		return failure(call, err.Error())
	}

	maxDepth := intArg(call.Arguments, "depth", defaultListDepth)
	if maxDepth < 0 {
		maxDepth = defaultListDepth
	}
	limit := intArg(call.Arguments, "limit_entries", defaultListLimitEntries)
	if limit < 0 {
		limit = defaultListLimitEntries
	}
	if limit > maxListLimitEntries {
		limit = maxListLimitEntries
	}
	includeHidden := boolArg(call.Arguments, "include_hidden", false)

	type listEntry struct {
		Path  string `json:"path"`
		Type  string `json:"type"`
		Size  int64  `json:"size,omitempty"`
		Depth int    `json:"depth"`
	}
	type listRequest struct {
		Path          string `json:"path"`
		Depth         int    `json:"depth"`
		IncludeHidden bool   `json:"include_hidden"`
		LimitEntries  int    `json:"limit_entries"`
	}
	type listSummary struct {
		ReturnedEntries     int  `json:"returned_entries"`
		MatchedEntries      int  `json:"matched_entries"`
		OmittedEntries      int  `json:"omitted_entries"`
		OmittedEntriesExact bool `json:"omitted_entries_exact"`
		HiddenOmitted       int  `json:"hidden_omitted"`
		ScannedEntries      int  `json:"scanned_entries"`
		ScanLimit           int  `json:"scan_limit"`
		ScanTruncated       bool `json:"scan_truncated"`
		Truncated           bool `json:"truncated"`
		Directories         int  `json:"directories"`
		Files               int  `json:"files"`
		Symlinks            int  `json:"symlinks"`
		Other               int  `json:"other"`
		MaxDepth            int  `json:"max_depth"`
	}
	type listResult struct {
		Root    string      `json:"root"`
		Request listRequest `json:"request"`
		Summary listSummary `json:"summary"`
		Entries []listEntry `json:"entries"`
	}
	results := make([]listEntry, 0, minListEntries(limit, defaultListLimitEntries))
	summary := listSummary{MaxDepth: maxDepth, ScanLimit: maxListScanEntries, OmittedEntriesExact: true}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		depth := strings.Count(rel, string(filepath.Separator))
		if depth > maxDepth {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if summary.ScannedEntries >= maxListScanEntries {
			summary.ScanTruncated = true
			summary.OmittedEntriesExact = false
			return errListScanLimit
		}
		summary.ScannedEntries++
		if !includeHidden && strings.HasPrefix(d.Name(), ".") {
			summary.HiddenOmitted++
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		itemType := "file"
		if d.IsDir() {
			itemType = "directory"
		} else if info.Mode()&os.ModeSymlink != 0 {
			itemType = "symlink"
		} else if !info.Mode().IsRegular() {
			itemType = "other"
		}
		summary.MatchedEntries++
		switch itemType {
		case "directory":
			summary.Directories++
		case "file":
			summary.Files++
		case "symlink":
			summary.Symlinks++
		default:
			summary.Other++
		}
		if len(results) < limit {
			rel := filepath.ToSlash(rel)
			results = append(results, listEntry{Path: rel, Type: itemType, Size: info.Size(), Depth: depth})
		}
		return nil
	})
	if err != nil && !errors.Is(err, errListScanLimit) {
		return failure(call, err.Error())
	}

	summary.ReturnedEntries = len(results)
	if summary.MatchedEntries > summary.ReturnedEntries {
		summary.OmittedEntries = summary.MatchedEntries - summary.ReturnedEntries
		summary.Truncated = true
	}
	if summary.ScanTruncated {
		summary.Truncated = true
	}

	return marshalSuccess(call, listResult{
		Root: root,
		Request: listRequest{
			Path:          path,
			Depth:         maxDepth,
			IncludeHidden: includeHidden,
			LimitEntries:  limit,
		},
		Summary: summary,
		Entries: results,
	})
}

func (t *statTool) Execute(ctx context.Context, call domain.ToolCall) domain.ToolResult {
	path, ok := stringArg(call.Arguments, "path")
	if !ok {
		return failure(call, "path パラメータが必要です")
	}
	if err := authorize(ctx, t.engine, t.approver, call); err != nil {
		return failure(call, err.Error())
	}

	resolved, err := t.paths.ResolveWritableFile(path)
	if err != nil {
		return failure(call, err.Error())
	}
	info, err := os.Lstat(resolved)
	if err != nil {
		return failure(call, err.Error())
	}

	result := map[string]any{
		"path":        resolved,
		"name":        info.Name(),
		"size":        info.Size(),
		"mode":        info.Mode().String(),
		"modified_at": info.ModTime().Format("2006-01-02T15:04:05Z07:00"),
		"is_dir":      info.IsDir(),
		"is_symlink":  info.Mode()&os.ModeSymlink != 0,
	}
	return marshalSuccess(call, result)
}

func (t *removeTool) Execute(ctx context.Context, call domain.ToolCall) domain.ToolResult {
	path, ok := stringArg(call.Arguments, "path")
	if !ok {
		return failure(call, "path パラメータが必要です")
	}
	if err := authorize(ctx, t.engine, t.approver, call); err != nil {
		return failure(call, err.Error())
	}

	resolved, kind, err := t.paths.EnsureRemovable(path, boolArg(call.Arguments, "recursive", false))
	if err != nil {
		return failure(call, err.Error())
	}
	if kind == "directory" {
		if err := os.RemoveAll(resolved); err != nil {
			return failure(call, err.Error())
		}
		return success(call, fmt.Sprintf("ディレクトリ %s を削除しました", resolved))
	}
	if err := os.Remove(resolved); err != nil {
		return failure(call, err.Error())
	}
	return success(call, fmt.Sprintf("ファイル %s を削除しました", resolved))
}

func (t *moveTool) Execute(ctx context.Context, call domain.ToolCall) domain.ToolResult {
	src, ok := stringArg(call.Arguments, "source_path")
	if !ok {
		return failure(call, "source_path パラメータが必要です")
	}
	dst, ok := stringArg(call.Arguments, "destination_path")
	if !ok {
		return failure(call, "destination_path パラメータが必要です")
	}
	if err := authorize(ctx, t.engine, t.approver, call); err != nil {
		return failure(call, err.Error())
	}

	resolvedSrc, resolvedDst, err := t.paths.ResolveMove(src, dst)
	if err != nil {
		return failure(call, err.Error())
	}
	if err := os.Rename(resolvedSrc, resolvedDst); err != nil {
		return failure(call, err.Error())
	}
	return success(call, fmt.Sprintf("%s を %s に移動しました", resolvedSrc, resolvedDst))
}

func authorize(ctx context.Context, engine domain.PolicyEngine, approver domain.Approver, call domain.ToolCall) error {
	if engine == nil || approver == nil {
		return nil
	}
	decision, request, err := engine.Evaluate(ctx, call)
	if err != nil {
		return err
	}
	execctx.FillPermissionRequest(ctx, &request)
	if decision == domain.PolicyAllow {
		return nil
	}
	if decision == domain.PolicyDeny {
		return fmt.Errorf("この操作は policy により拒否されました")
	}
	userDecision, err := approver.Approve(ctx, request)
	if err != nil {
		return err
	}
	if userDecision == domain.PermissionDeny {
		return fmt.Errorf("ユーザーによってキャンセルされました")
	}
	return nil
}

func authorizeWrite(ctx context.Context, engine domain.PolicyEngine, approver domain.Approver, call domain.ToolCall, resolved string, before []byte, beforeExists bool, content string) error {
	if engine == nil || approver == nil {
		return nil
	}
	decision, request, err := engine.Evaluate(ctx, call)
	if err != nil {
		return err
	}
	execctx.FillPermissionRequest(ctx, &request)
	if decision == domain.PolicyAllow {
		return nil
	}
	if decision == domain.PolicyDeny {
		return fmt.Errorf("この操作は policy により拒否されました")
	}
	request.PreviewKind = "diff"
	request.Preview = diffpreview.TextChange(resolved, before, beforeExists, content)
	stats := diffpreview.TextChangeStats(before, beforeExists, content)
	request.ChangeFiles = stats.Files
	request.Additions = stats.Additions
	request.Deletions = stats.Deletions
	userDecision, err := approver.Approve(ctx, request)
	if err != nil {
		return err
	}
	if userDecision == domain.PermissionDeny {
		return fmt.Errorf("ユーザーによってキャンセルされました")
	}
	return nil
}

func stringSchema(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

func numberSchema(desc string) map[string]any {
	return map[string]any{"type": "integer", "description": desc}
}

func boolSchema(desc string) map[string]any {
	return map[string]any{"type": "boolean", "description": desc}
}

func stringArg(args map[string]any, key string) (string, bool) {
	value, ok := args[key].(string)
	return value, ok && value != ""
}

func intArg(args map[string]any, key string, fallback int) int {
	value, ok := args[key]
	if !ok {
		return fallback
	}
	switch n := value.(type) {
	case float64:
		return int(n)
	case int:
		return n
	default:
		return fallback
	}
}

func boolArg(args map[string]any, key string, fallback bool) bool {
	value, ok := args[key].(bool)
	if !ok {
		return fallback
	}
	return value
}

func minListEntries(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func success(call domain.ToolCall, output string) domain.ToolResult {
	return domain.ToolResult{CallID: call.ID, Name: call.Name, Success: true, Output: output}
}

func marshalSuccess(call domain.ToolCall, value any) domain.ToolResult {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return failure(call, err.Error())
	}
	return success(call, string(data))
}

func failure(call domain.ToolCall, output string) domain.ToolResult {
	return domain.ToolResult{CallID: call.ID, Name: call.Name, Success: false, Output: "エラー: " + output}
}
