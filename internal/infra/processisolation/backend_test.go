package processisolation

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"yagent/internal/config"
)

func TestBackendWrapEncodesProcessIsolationRequest(t *testing.T) {
	backend, err := New(config.ProcessIsolationConfig{Runner: "vm-proxy", Args: []string{"--socket", "/tmp/vm.sock"}})
	if err != nil {
		t.Fatal(err)
	}
	spec, err := backend.Wrap(Request{Mode: ModeCommand, Command: "go", Args: []string{"test", "./..."}, Cwd: "/workspace", ReadPaths: []string{"/workspace"}, WritePaths: []string{"/workspace/tmp"}})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Command != "vm-proxy" || len(spec.Args) < 5 || spec.Args[2] != "--yagent-process-spec" || spec.Args[4] != "--" || spec.Args[5] != "go" {
		t.Fatalf("unexpected command spec: %+v", spec)
	}
	payload, err := base64.RawURLEncoding.DecodeString(spec.Args[3])
	if err != nil {
		t.Fatal(err)
	}
	var request Request
	if err := json.Unmarshal(payload, &request); err != nil {
		t.Fatal(err)
	}
	if request.Protocol != ProtocolV1 || request.Mode != ModeCommand || request.Command != "go" || request.Cwd != "/workspace" || len(request.WritePaths) != 1 {
		t.Fatalf("unexpected encoded request: %+v", request)
	}
}

func TestBackendRejectsAmbiguousConfiguration(t *testing.T) {
	if _, err := New(config.ProcessIsolationConfig{Backend: "macos-sandbox-exec", Runner: "vm-proxy"}); err == nil {
		t.Fatal("expected backend and runner conflict")
	}
}
