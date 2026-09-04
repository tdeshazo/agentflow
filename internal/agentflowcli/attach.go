package agentflowcli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/tdeshazo/agentflow/internal/clioutput"
	"github.com/tdeshazo/agentflow/internal/supervision"
)

const maxAttachReplay = 200

func runAttach(repoRoot, workflowName string, replay int, in io.Reader, out io.Writer) error {
	return runAttachWithHook(repoRoot, workflowName, replay, in, out, nil)
}

func runAttachWithHook(repoRoot, workflowName string, replay int, in io.Reader, out io.Writer, attached func() error) error {
	if replay < 0 || replay > maxAttachReplay {
		return fmt.Errorf("--replay must be between 0 and %d", maxAttachReplay)
	}
	repo, err := targetRepo(repoRoot)
	if err != nil {
		return err
	}
	client, err := supervision.Attach(context.Background(), repo, workflowName, replay)
	if err != nil {
		return err
	}
	defer client.Close()
	if attached != nil {
		if err := attached(); err != nil {
			return err
		}
	}
	raw := clioutput.NewPresenterWithPresentation(out, clioutput.PresentationRaw)

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	return attachLive(context.Background(), client, signals, in, raw.Out)
}

func attachLive(ctx context.Context, client *supervision.Client, signals <-chan os.Signal, in io.Reader, out io.Writer) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	closed := make(chan error, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			frame, err := client.Receive()
			if err != nil {
				closed <- err
				return
			}
			if len(frame.Output) != 0 {
				if _, err := out.Write(frame.Output); err != nil {
					closed <- err
					return
				}
			}
			if frame.Completed {
				if frame.Result != "success" {
					closed <- fmt.Errorf("supervised workflow completed with result %q", frame.Result)
				} else {
					closed <- nil
				}
				return
			}
			if frame.Detached {
				closed <- nil
				return
			}
		}
	}()
	defer func() {
		cancel()
		_ = client.Close()
		wg.Wait()
	}()

	inputErr := make(chan error, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		inputErr <- forwardAttachInput(ctx, in, client)
	}()
	signalDone := ctx.Done()
	for {
		select {
		case err := <-closed:
			return err
		case err := <-inputErr:
			// EOF from a pipe is normal: output and signals remain attached.
			if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, context.Canceled) {
				return err
			}
			if errors.Is(err, io.EOF) {
				if err := client.RequestDetach(); err != nil {
					return err
				}
			}
			inputErr = nil
		case <-signalDone:
			if err := client.SendSignal("interrupt"); err != nil {
				return err
			}
			// The session, not socket EOF, owns termination. Keep draining until
			// the server's authoritative completion frame arrives.
			signalDone = nil
		case received := <-signals:
			name := "interrupt"
			if received == syscall.SIGTERM {
				name = "terminate"
			}
			if err := client.SendSignal(name); err != nil {
				return err
			}
			signals = nil
		}
	}
}
