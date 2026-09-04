package observability

import (
	"bufio"
	"errors"
	"io"
	"os"
	"sync"
)

// OutputBridge captures process stdout and stderr as operational log events.
// It is used only by detached children; output is diagnostic and never feeds
// workflow acceptance or state transitions.
type OutputBridge struct {
	store       *LogStore
	stdoutRead  *os.File
	stdoutWrite *os.File
	stderrRead  *os.File
	stderrWrite *os.File
	wg          sync.WaitGroup
	mu          sync.RWMutex
	live        func(string, []byte)
}

// SetLiveSink installs the authenticated session's live output sink. The sink
// is independent of diagnostic persistence so a full log cannot drop terminal
// output.
func (b *OutputBridge) SetLiveSink(sink func(string, []byte)) {
	b.mu.Lock()
	b.live = sink
	b.mu.Unlock()
}

// NewOutputBridge creates independent stdout and stderr pipes. The caller must
// call Close after restoring any process-global stdout/stderr assignments.
func NewOutputBridge(store *LogStore) (*OutputBridge, error) {
	stdoutRead, stdoutWrite, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	stderrRead, stderrWrite, err := os.Pipe()
	if err != nil {
		_ = stdoutRead.Close()
		_ = stdoutWrite.Close()
		return nil, err
	}
	bridge := &OutputBridge{store: store, stdoutRead: stdoutRead, stdoutWrite: stdoutWrite, stderrRead: stderrRead, stderrWrite: stderrWrite}
	bridge.wg.Add(2)
	go bridge.consume("stdout", stdoutRead)
	go bridge.consume("stderr", stderrRead)
	return bridge, nil
}

// Stdout and Stderr are the process descriptors to which detached runtime and
// provider output should be directed.
func (b *OutputBridge) Stdout() *os.File { return b.stdoutWrite }
func (b *OutputBridge) Stderr() *os.File { return b.stderrWrite }

// Close drains both streams and closes the bridge descriptors.
func (b *OutputBridge) Close() error {
	if b == nil {
		return nil
	}
	_ = b.stdoutWrite.Close()
	_ = b.stderrWrite.Close()
	b.wg.Wait()
	return errors.Join(b.stdoutRead.Close(), b.stderrRead.Close())
}

func (b *OutputBridge) consume(stream string, read *os.File) {
	defer b.wg.Done()
	reader := bufio.NewReader(read)
	for {
		data, err := reader.ReadBytes('\n')
		if len(data) > 0 {
			_ = b.store.Event("process_output", map[string]string{"stream": stream, "data": string(data)})
			b.mu.RLock()
			live := b.live
			b.mu.RUnlock()
			if live != nil {
				live(stream, data)
			}
		}
		if errors.Is(err, io.EOF) {
			return
		}
		if err != nil {
			return
		}
	}
}
