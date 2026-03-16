package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"yagent/internal/app"
	"yagent/internal/domain"
)

func newExecCommand(configPath *string, logPath *string) *cobra.Command {
	var prompt string
	var model string
	var stream bool

	command := &cobra.Command{
		Use:   "exec",
		Short: "単発でプロンプトを実行",
		RunE: func(cmd *cobra.Command, args []string) error {
			container, err := app.Build(*configPath, NewStdinApprover(), app.BuildOptions{LogPath: *logPath})
			if err != nil {
				return err
			}
			if container.Closer != nil {
				defer container.Closer.Close()
			}

			result, err := container.Orchestrator.RunTurn(cmd.Context(), domain.TurnRequest{
				Messages: []domain.Message{{Role: domain.RoleUser, Content: prompt}},
				Model:    model,
				Stream:   stream,
			})
			if err != nil {
				return err
			}

			fmt.Println(result.Message.Content)
			return nil
		},
	}

	command.Flags().StringVar(&prompt, "prompt", "", "AI に送信するプロンプト")
	command.Flags().StringVar(&model, "model", "", "使用するモデル名")
	command.Flags().BoolVar(&stream, "stream", false, "ストリーミング応答を有効にする")
	_ = command.MarkFlagRequired("prompt")

	return command
}

type stdinPatternApproval struct {
	toolName     string
	action       string
	resourceKind string
	risk         string
	pattern      string
}

type StdinApprover struct {
	sessionApprovals map[string]bool
	patternApprovals []stdinPatternApproval
}

func NewStdinApprover() *StdinApprover {
	return &StdinApprover{
		sessionApprovals: map[string]bool{},
	}
}

func (a *StdinApprover) Approve(ctx context.Context, request domain.PermissionRequest) (domain.PermissionDecision, error) {
	key := cliApprovalKey(request)
	if a.sessionApprovals[key] || a.hasPatternApproval(request) {
		return domain.PermissionAllowSession, nil
	}

	fmt.Printf("%sを実行しますか？ファイル：%s\n", request.Operation, request.Resource)
	fmt.Printf("requester: %s (%s)\n", cliPermissionRequesterLabel(request), cliPermissionRequesterType(request))
	if request.Purpose != "" {
		fmt.Printf("purpose: %s\n", request.Purpose)
	}
	if domain.PermissionRequestSupportsPatternApproval(request) {
		fmt.Print("[1] 今回だけ許可  [2] 同じ操作を以後許可  [3] ファイルパターン指定で以後許可  [4] 拒否: ")
	} else {
		fmt.Print("[1] 今回だけ許可  [2] 同じ操作を以後許可  [3] 拒否: ")
	}
	var input string
	fmt.Scanln(&input)
	switch input {
	case "1":
		return domain.PermissionAllowOnce, nil
	case "2":
		a.sessionApprovals[key] = true
		return domain.PermissionAllowSession, nil
	case "3":
		if domain.PermissionRequestSupportsPatternApproval(request) {
			fmt.Print("許可するパターン (例: *.go / internal/*): ")
			var patternValue string
			fmt.Scanln(&patternValue)
			patternValue = strings.TrimSpace(patternValue)
			if patternValue == "" {
				return domain.PermissionDeny, nil
			}
			a.patternApprovals = append(a.patternApprovals, stdinPatternApproval{
				toolName:     request.ToolName,
				action:       request.Action,
				resourceKind: request.ResourceKind,
				risk:         request.Risk,
				pattern:      patternValue,
			})
			return domain.PermissionAllowSession, nil
		}
		return domain.PermissionDeny, nil
	case "4":
		if domain.PermissionRequestSupportsPatternApproval(request) {
			return domain.PermissionDeny, nil
		}
		return domain.PermissionDeny, nil
	default:
		return domain.PermissionDeny, nil
	}
}

func cliApprovalKey(request domain.PermissionRequest) string {
	return strings.Join([]string{
		request.ToolName,
		request.Action,
		request.ResourceKind,
		request.Scope,
		request.Risk,
	}, "\x00")
}

func (a *StdinApprover) hasPatternApproval(request domain.PermissionRequest) bool {
	for _, approval := range a.patternApprovals {
		if approval.toolName != request.ToolName || approval.action != request.Action || approval.resourceKind != request.ResourceKind || approval.risk != request.Risk {
			continue
		}
		if domain.PermissionRequestMatchesPattern(request, approval.pattern) {
			return true
		}
	}
	return false
}

func cliPermissionRequesterLabel(request domain.PermissionRequest) string {
	if request.AgentID == "" || request.AgentID == "manager" {
		return "manager"
	}
	return request.AgentID
}

func cliPermissionRequesterType(request domain.PermissionRequest) string {
	if request.AgentID == "" || request.AgentID == "manager" {
		return "main"
	}
	return "subagent"
}
