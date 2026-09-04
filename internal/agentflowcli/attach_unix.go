//go:build linux

package agentflowcli

import (
	"context"
	"fmt"
	"io"
	"os"
	"syscall"
	"time"

	"github.com/tdeshazo/agentflow/internal/supervision"
)

func forwardAttachInput(ctx context.Context, in io.Reader, client *supervision.Client) error {
	file, ok := in.(*os.File)
	if !ok {
		return forwardAttachReader(ctx, in, client)
	}
	fd := int(file.Fd())
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		var readSet syscall.FdSet
		readSet.Bits[fd/64] |= 1 << uint(fd%64)
		timeout := syscall.NsecToTimeval((100 * time.Millisecond).Nanoseconds())
		ready, err := syscall.Select(fd+1, &readSet, nil, nil, &timeout)
		if err != nil {
			return fmt.Errorf("wait for attach input: %w", err)
		}
		if ready == 0 {
			continue
		}
		buffer := make([]byte, 4096)
		count, err := file.Read(buffer)
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

func forwardAttachReader(ctx context.Context, in io.Reader, client *supervision.Client) error {
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
