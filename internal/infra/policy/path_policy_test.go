package policy

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPathPolicyRejectsOutsideResolvedSymlink(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	target := filepath.Join(outside, "a.txt")
	if err := os.WriteFile(target, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	p := NewPathPolicy(root, []string{root})
	if _, err := p.ResolveFile(link); err == nil {
		t.Fatalf("expected symlink escape to be rejected")
	}
}
