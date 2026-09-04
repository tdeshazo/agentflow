//go:build !linux

package agentflowcli

import (
	"context"
	"io"

	"github.com/tdeshazo/agentflow/internal/supervision"
)

func forwardAttachInput(ctx context.Context, in io.Reader, client *supervision.Client) error {
	buffer := make([]byte, 4096)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		count, err := in.Read(buffer)
		if count > 0 {
			if sendErr := client.SendInput(string(buffer[:count])); sendErr != nil {
				return sendErr
			}
		}
		if err != nil {
			return err
		}
	}
}
