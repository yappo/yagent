package processisolation

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"yagent/internal/config"
)

func TestMacSandboxBackendAllowsDeclaredWritesOnly(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS sandbox backend")
	}
	if _, err := exec.LookPath("sandbox-exec"); err != nil {
		t.Skip("sandbox-exec unavailable")
	}
	root := t.TempDir()
	allowed := filepath.Join(root, "allowed.txt")
	forbidden := filepath.Join(root, "forbidden.txt")
	backend, err := New(config.ProcessIsolationConfig{Backend: "macos-sandbox-exec"})
	if err != nil {
		t.Fatal(err)
	}
	spec, err := backend.Wrap(Request{Mode: ModeCommand, Command: "/bin/sh", Args: []string{"-c", "touch \"$1\"; touch \"$2\"", "sh"}, Cwd: root, WritePaths: []string{allowed}})
	if err != nil {
		t.Fatal(err)
	}
	// The profile allows the directory but not the sibling file path itself;
	// the command must fail closed before it can create the undeclared file.
	cmd := exec.Command(spec.Command, append(spec.Args, allowed, forbidden)...)
	cmd.Dir = spec.Cwd
	if err := cmd.Run(); err == nil {
		t.Fatal("sandboxed command unexpectedly succeeded")
	}
	if _, err := os.Stat(forbidden); err == nil {
		t.Fatal("sandboxed command wrote undeclared path")
	}
}

func TestMacSandboxBackendRejectsUndeclaredReads(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS sandbox backend")
	}
	if _, err := exec.LookPath("sandbox-exec"); err != nil {
		t.Skip("sandbox-exec unavailable")
	}
	root := t.TempDir()
	secret := filepath.Join(filepath.Dir(root), "yagent-process-isolation-secret.txt")
	if err := os.WriteFile(secret, []byte("host secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(secret) })
	backend, err := New(config.ProcessIsolationConfig{Backend: "macos-sandbox-exec"})
	if err != nil {
		t.Fatal(err)
	}
	spec, err := backend.Wrap(Request{Mode: ModeCommand, Command: "/bin/cat", Args: []string{secret}, Cwd: root, ReadPaths: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(spec.Command, spec.Args...)
	cmd.Dir = spec.Cwd
	if err := cmd.Run(); err == nil {
		t.Fatal("sandboxed command read an undeclared path")
	}
}
