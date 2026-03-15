package policy

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"yagent/internal/domain"
)

type PathPolicy struct {
	baseDir      string
	allowedRoots []string
}

func NewPathPolicy(baseDir string, allowedRoots []string) *PathPolicy {
	roots := make([]string, 0, len(allowedRoots))
	seen := map[string]struct{}{}
	for _, root := range allowedRoots {
		if root == "" {
			continue
		}
		absRoot, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		realRoot, err := filepath.EvalSymlinks(absRoot)
		if err == nil {
			absRoot = realRoot
		}
		if _, ok := seen[absRoot]; ok {
			continue
		}
		seen[absRoot] = struct{}{}
		roots = append(roots, absRoot)
	}

	return &PathPolicy{
		baseDir:      baseDir,
		allowedRoots: roots,
	}
}

var _ domain.PathPolicy = (*PathPolicy)(nil)

func (p *PathPolicy) ResolveFile(path string) (string, error) {
	absPath, err := p.resolveInputPath(path)
	if err != nil {
		return "", err
	}

	info, err := os.Lstat(absPath)
	if err != nil {
		return "", fmt.Errorf("ファイル情報の取得に失敗しました: %w", err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("ファイルではありません: %s", absPath)
	}

	realPath, err := p.evalPath(absPath)
	if err != nil {
		return "", err
	}
	if err := p.ensureAllowed(realPath); err != nil {
		return "", err
	}

	return realPath, nil
}

func (p *PathPolicy) ResolveDir(path string) (string, error) {
	absPath, err := p.resolveInputPath(path)
	if err != nil {
		return "", err
	}

	info, err := os.Lstat(absPath)
	if err != nil {
		return "", fmt.Errorf("ディレクトリ情報の取得に失敗しました: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("ディレクトリではありません: %s", absPath)
	}

	realPath, err := p.evalPath(absPath)
	if err != nil {
		return "", err
	}
	if err := p.ensureAllowed(realPath); err != nil {
		return "", err
	}

	return realPath, nil
}

func (p *PathPolicy) ResolveSearchRoot(path string) (string, error) {
	return p.ResolveDir(path)
}

func (p *PathPolicy) ResolveWritableFile(path string) (string, error) {
	absPath, err := p.resolveInputPath(path)
	if err != nil {
		return "", err
	}

	parent := filepath.Dir(absPath)
	realParent, err := p.evalPath(parent)
	if err != nil {
		return "", err
	}
	if err := p.ensureAllowed(realParent); err != nil {
		return "", err
	}

	if info, err := os.Lstat(absPath); err == nil {
		if info.IsDir() {
			return "", fmt.Errorf("ファイルパスではありません: %s", absPath)
		}
		realPath, err := p.evalPath(absPath)
		if err != nil {
			return "", err
		}
		if err := p.ensureAllowed(realPath); err != nil {
			return "", err
		}
		return realPath, nil
	}

	return filepath.Join(realParent, filepath.Base(absPath)), nil
}

func (p *PathPolicy) ResolveMove(src, dst string) (string, string, error) {
	resolvedSrc, err := p.ResolveFile(src)
	if err != nil {
		return "", "", err
	}
	resolvedDst, err := p.ResolveWritableFile(dst)
	if err != nil {
		return "", "", err
	}
	return resolvedSrc, resolvedDst, nil
}

func (p *PathPolicy) EnsureRemovable(path string, recursive bool) (string, string, error) {
	absPath, err := p.resolveInputPath(path)
	if err != nil {
		return "", "", err
	}

	info, err := os.Lstat(absPath)
	if err != nil {
		return "", "", fmt.Errorf("削除対象の確認に失敗しました: %w", err)
	}

	realPath, err := p.evalPath(absPath)
	if err != nil {
		return "", "", err
	}
	if err := p.ensureAllowed(realPath); err != nil {
		return "", "", err
	}

	for _, root := range p.allowedRoots {
		if samePath(realPath, root) {
			return "", "", fmt.Errorf("許可 root 自体は削除できません: %s", realPath)
		}
	}

	if info.IsDir() {
		if !recursive {
			return "", "", fmt.Errorf("ディレクトリ削除には recursive=true が必要です: %s", realPath)
		}
		if p.relativeDepth(realPath) < 2 {
			return "", "", fmt.Errorf("許可 root に近すぎるディレクトリは削除できません: %s", realPath)
		}
		return realPath, "directory", nil
	}

	return realPath, "file", nil
}

func (p *PathPolicy) resolveInputPath(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("path パラメータが必要です")
	}

	resolved := path
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(p.baseDir, resolved)
	}

	absPath, err := filepath.Abs(filepath.Clean(resolved))
	if err != nil {
		return "", fmt.Errorf("パスの解決に失敗しました: %w", err)
	}
	return absPath, nil
}

func (p *PathPolicy) evalPath(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("パスが存在しないか、symlink の解決に失敗しました: %s", path)
		}
		return "", fmt.Errorf("symlink の解決に失敗しました: %w", err)
	}
	return resolved, nil
}

func (p *PathPolicy) ensureAllowed(path string) error {
	for _, root := range p.allowedRoots {
		if withinRoot(path, root) {
			return nil
		}
	}
	return fmt.Errorf("アクセスが許可されていないパスです: %s", path)
}

func (p *PathPolicy) relativeDepth(path string) int {
	minDepth := 1 << 30
	for _, root := range p.allowedRoots {
		if !withinRoot(path, root) {
			continue
		}
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == "." {
			if 0 < minDepth {
				minDepth = 0
			}
			continue
		}
		parts := strings.Split(rel, string(filepath.Separator))
		if len(parts) < minDepth {
			minDepth = len(parts)
		}
	}
	if minDepth == 1<<30 {
		return 0
	}
	return minDepth
}

func withinRoot(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func samePath(a, b string) bool {
	return filepath.Clean(a) == filepath.Clean(b)
}
