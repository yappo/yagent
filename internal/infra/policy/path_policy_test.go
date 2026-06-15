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

func TestPathPolicyDenyRuleOverridesAllowedRoot(t *testing.T) {
	root := t.TempDir()
	secretDir := filepath.Join(root, "secret")
	if err := os.Mkdir(secretDir, 0o755); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(secretDir, "token.txt")
	if err := os.WriteFile(secret, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}

	p := NewPathPolicyWithRules(root, []string{root}, []PathRule{
		{Decision: PathDecisionDeny, Patterns: []string{"secret"}},
	})
	if _, err := p.ResolveFile(secret); err == nil {
		t.Fatalf("expected deny rule to reject path under allowed root")
	}
}

func TestPathPolicyDenyGlobRejectsWritableFile(t *testing.T) {
	root := t.TempDir()
	p := NewPathPolicyWithRules(root, []string{root}, []PathRule{
		{Decision: PathDecisionDeny, Patterns: []string{"*.pem"}},
	})

	if _, err := p.ResolveWritableFile(filepath.Join(root, "private.pem")); err == nil {
		t.Fatalf("expected deny glob to reject new writable file")
	}
}

func TestPathPolicyAllowRuleAddsExplicitPathOutsideAllowedRoot(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	file := filepath.Join(outside, "notes.txt")
	if err := os.WriteFile(file, []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}

	p := NewPathPolicyWithRules(root, []string{root}, []PathRule{
		{Decision: PathDecisionAllow, Patterns: []string{filepath.Join(outside, "*.txt")}},
	})
	want, err := filepath.EvalSymlinks(file)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := p.ResolveFile(file); err != nil || got != want {
		t.Fatalf("expected allow rule to permit outside file, got path=%q want=%q err=%v", got, want, err)
	}
}
