package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"yagent/internal/app"
	"yagent/internal/domain"
	chatusecase "yagent/internal/usecase/chat"
)

func newExecCommand(configPath *string) *cobra.Command {
	var prompt string
	var model string
	var stream bool

	command := &cobra.Command{
		Use:   "exec",
		Short: "単発でプロンプトを実行",
		RunE: func(cmd *cobra.Command, args []string) error {
			container, err := app.Build(*configPath, StdinApprover{})
			if err != nil {
				return err
			}

			result, err := container.ChatService.Run(cmd.Context(), chatusecase.Input{
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

type StdinApprover struct{}

func (StdinApprover) Approve(ctx context.Context, request domain.PermissionRequest) (domain.PermissionDecision, error) {
	fmt.Printf("%sを実行しますか？ファイル：%s\n", request.Operation, request.Resource)
	fmt.Print("[1] 今回だけ許可  [2] このセッションで許可  [3] 拒否: ")
	var input string
	fmt.Scanln(&input)
	switch input {
	case "1":
		return domain.PermissionAllowOnce, nil
	case "2":
		return domain.PermissionAllowSession, nil
	default:
		return domain.PermissionDeny, nil
	}
}
