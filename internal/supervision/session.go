// Package supervision owns private, local terminal handoff for a live
// detached AgentFlow run. It is intentionally separate from Git-backed
// acceptance state: a session is operational plumbing, never authority.
package supervision

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tdeshazo/agentflow/internal/gitstate"
)

const (
	metadataVersion   = 1
	protocolVersion   = 1
	maxInputBytes     = 4096
	maxReplayBytes    = 1 << 20
	maxOutgoingFrames = 1024
	unixSocketPathMax = 103
	handshakeTimeout  = 5 * time.Second
	writeTimeout      = 2 * time.Second
)

// ErrUnavailable means the host cannot provide the local IPC primitive needed
// for supervised attachment. Callers may keep a detached run running, but
// must never advertise it as attachable or fall back to weaker identity.
var ErrUnavailable = errors.New("supervised session IPC is unavailable")

// Metadata binds one private IPC endpoint to a specific live workflow run.
// It intentionally carries no workflow inputs, prompts, output, or secrets.
type Metadata struct {
	Version  int                       `json:"version"`
	Workflow string                    `json:"workflow"`
	RunID    string                    `json:"run_id"`
	Process  *gitstate.ProcessMetadata `json:"process"`
	Socket   string                    `json:"socket"`
}

type message struct {
	Version int    `json:"version"`
	Type    string `json:"type"`
	RunID   string `json:"run_id,omitempty"`
	Signal  string `json:"signal,omitempty"`
	Data    string `json:"data,omitempty"`
	Error   string `json:"error,omitempty"`
	Stream  string `json:"stream,omitempty"`
	Cursor  uint64 `json:"cursor,omitempty"`
	Result  string `json:"result,omitempty"`
	Replay  int    `json:"replay,omitempty"`
}

type replayChunk struct {
	stream string
	data   string
	cursor uint64
}

type connection struct {
	conn     net.Conn
	encoder  *json.Encoder
	mu       sync.Mutex
	outgoing chan outbound
	closed   bool
	done     chan struct{}
}

type outbound struct {
	message message
	written chan error
}

func newConnection(conn net.Conn) *connection {
	c := &connection{conn: conn, encoder: json.NewEncoder(conn), outgoing: make(chan outbound, maxOutgoingFrames), done: make(chan struct{})}
	go c.writeLoop()
	return c
}

func (c *connection) enqueue(value message) bool {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return false
	}
	select {
	case c.outgoing <- outbound{message: value}:
		c.mu.Unlock()
		return true
	default:
		c.closed = true
		close(c.outgoing)
		c.mu.Unlock()
		_ = c.conn.Close()
		return false
	}
}

func (c *connection) enqueueAndWait(value message) error {
	written := make(chan error, 1)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return net.ErrClosed
	}
	select {
	case c.outgoing <- outbound{message: value, written: written}:
		c.mu.Unlock()
	case <-c.done:
		c.mu.Unlock()
		return net.ErrClosed
	}
	select {
	case err := <-written:
		return err
	case <-c.done:
		return net.ErrClosed
	}
}

func (c *connection) abort() {
	c.mu.Lock()
	if !c.closed {
		c.closed = true
		close(c.outgoing)
	}
	c.mu.Unlock()
	_ = c.conn.Close()
	<-c.done
}

func (c *connection) writeLoop() {
	defer close(c.done)
	for item := range c.outgoing {
		_ = c.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
		err := c.encoder.Encode(item.message)
		if item.written != nil {
			item.written <- err
		}
		if err != nil {
			_ = c.conn.Close()
			return
		}
	}
}

// Server owns one session listener. At most one attachment is admitted at a
// time; closing the server cancels all connection handlers and removes its
// own metadata only when it still describes the current process.
type Server struct {
	repo        gitstate.Repo
	metadata    Metadata
	listener    net.Listener
	cancelRun   context.CancelFunc
	mu          sync.Mutex
	inputGate   *inputGeneration
	attached    bool
	connections map[*connection]struct{}
	active      *connection
	replay      []replayChunk
	replayBytes int
	cursor      uint64
	closed      bool
	wg          sync.WaitGroup
}

type inputGeneration struct {
	data chan []byte
	done chan struct{}
}

type sessionInput struct{ server *Server }

func (r sessionInput) Read(p []byte) (int, error) { return r.server.readInput(p) }

// Start creates a private, local session endpoint for a detached run.
// Process identity must be available because attach refuses PID-only identity.
func Start(repo gitstate.Repo, workflow, runID string, cancelRun context.CancelFunc) (*Server, error) {
	if workflow == "" || !validRunID(runID) || cancelRun == nil {
		return nil, fmt.Errorf("invalid supervised session identity")
	}
	process := gitstate.CurrentProcessMetadata()
	if process == nil {
		return nil, fmt.Errorf("%w: stable process identity is unavailable", ErrUnavailable)
	}
	metadataPath, socketPath, err := paths(repo, workflow)
	if err != nil {
		return nil, err
	}
	if err := prepareDirectory(filepath.Dir(metadataPath)); err != nil {
		return nil, err
	}
	if filepath.Dir(socketPath) != filepath.Dir(metadataPath) {
		if err := prepareDirectory(filepath.Dir(socketPath)); err != nil {
			return nil, err
		}
	}
	if err := removeVerifiedStale(metadataPath, socketPath); err != nil {
		return nil, err
	}
	listener, err := listenSession(socketPath)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(socketPath, 0o600); err != nil {
		_ = listener.Close()
		_ = os.Remove(socketPath)
		return nil, fmt.Errorf("restrict supervised session endpoint: %w", err)
	}
	metadata := Metadata{Version: metadataVersion, Workflow: workflow, RunID: runID, Process: process, Socket: socketPath}
	if err := writeMetadata(metadataPath, metadata); err != nil {
		_ = listener.Close()
		_ = os.Remove(socketPath)
		return nil, err
	}
	server := &Server{
		repo: repo, metadata: metadata, listener: listener, cancelRun: cancelRun,
		connections: make(map[*connection]struct{}),
	}
	server.wg.Add(1)
	go server.serve()
	return server, nil
}

// Input returns the only reader that receives attached operator input.
func (s *Server) Input() io.Reader { return sessionInput{server: s} }

// EnableInput permits attached input only while the engine is at a supported
// operator gate. Input is never buffered across lifecycle boundaries.
func (s *Server) EnableInput(enabled bool) {
	s.mu.Lock()
	previous := s.inputGate
	if enabled && !s.closed {
		s.inputGate = &inputGeneration{data: make(chan []byte, 1), done: make(chan struct{})}
	} else {
		s.inputGate = nil
	}
	s.mu.Unlock()
	if previous != nil {
		close(previous.done)
	}
}

func (s *Server) serve() {
	defer s.wg.Done()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		peer := newConnection(conn)
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			peer.abort()
			return
		}
		s.connections[peer] = struct{}{}
		s.mu.Unlock()
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.handle(peer)
		}()
	}
}

func (s *Server) handle(peer *connection) {
	conn := peer.conn
	defer func() {
		peer.abort()
		s.mu.Lock()
		delete(s.connections, peer)
		if s.active == peer {
			s.active = nil
			s.attached = false
		}
		s.mu.Unlock()
	}()
	_ = conn.SetReadDeadline(time.Now().Add(handshakeTimeout))
	decoder := json.NewDecoder(bufio.NewReader(conn))
	var hello message
	if err := decoder.Decode(&hello); err != nil {
		return
	}
	if hello.Version != protocolVersion || hello.Type != "attach" || hello.RunID != s.metadata.RunID {
		_ = peer.enqueueAndWait(message{Version: protocolVersion, Type: "rejected", Error: "session identity does not match the live run"})
		return
	}
	s.mu.Lock()
	if s.closed || s.attached {
		s.mu.Unlock()
		_ = peer.enqueueAndWait(message{Version: protocolVersion, Type: "rejected", Error: "a terminal is already attached to this run"})
		return
	}
	replay := append([]replayChunk(nil), s.replay...)
	if hello.Replay == 0 {
		replay = nil
	} else if hello.Replay > 0 && len(replay) > hello.Replay {
		replay = replay[len(replay)-hello.Replay:]
	}
	_ = conn.SetReadDeadline(time.Time{})
	if !peer.enqueue(message{Version: protocolVersion, Type: "attached", RunID: s.metadata.RunID}) {
		s.mu.Unlock()
		return
	}
	for _, chunk := range replay {
		if !peer.enqueue(message{Version: protocolVersion, Type: "output", RunID: s.metadata.RunID, Stream: chunk.stream, Data: chunk.data, Cursor: chunk.cursor}) {
			s.mu.Unlock()
			return
		}
	}
	s.attached = true
	s.active = peer
	s.mu.Unlock()
	for {
		var request message
		if err := decoder.Decode(&request); err != nil {
			return
		}
		if request.Version != protocolVersion || request.RunID != s.metadata.RunID {
			return
		}
		switch request.Type {
		case "input":
			if len(request.Data) == 0 || len(request.Data) > maxInputBytes {
				continue
			}
			s.deliverInput([]byte(request.Data))
		case "signal":
			if request.Signal != "interrupt" && request.Signal != "terminate" {
				continue
			}
			// Cancel is idempotent and is intentionally the only signal effect;
			// attach cannot send arbitrary signals to a provider process.
			s.cancelRun()
			s.EnableInput(false)
		case "detach":
			s.mu.Lock()
			cursor := s.cursor
			s.mu.Unlock()
			_ = peer.enqueueAndWait(message{Version: protocolVersion, Type: "detached", RunID: s.metadata.RunID, Cursor: cursor})
			return
		default:
			// The post-attach stream has no response multiplexing: ignore unknown
			// requests so a rejected input cannot be mistaken for a completed run.
			continue
		}
	}
}

func (s *Server) deliverInput(data []byte) {
	s.mu.Lock()
	gate := s.inputGate
	if s.closed || gate == nil {
		s.mu.Unlock()
		return
	}
	copyData := append([]byte(nil), data...)
	select {
	case gate.data <- copyData:
	default:
	}
	s.mu.Unlock()
}

func (s *Server) readInput(p []byte) (int, error) {
	s.mu.Lock()
	gate := s.inputGate
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return 0, io.EOF
	}
	if gate == nil {
		return 0, fmt.Errorf("supervised input requested outside an active human gate")
	}
	select {
	case data := <-gate.data:
		return copy(p, data), nil
	case <-gate.done:
		return 0, io.EOF
	}
}

// Publish retains a bounded per-run replay window and independently delivers
// live output. Reaching replay capacity evicts old chunks; it never suppresses
// the live stream.
func (s *Server) Publish(stream string, data []byte) {
	if s == nil || len(data) == 0 || (stream != "stdout" && stream != "stderr") {
		return
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.cursor++
	chunk := replayChunk{stream: stream, data: string(data), cursor: s.cursor}
	s.replay = append(s.replay, chunk)
	s.replayBytes += len(data)
	for s.replayBytes > maxReplayBytes && len(s.replay) > 0 {
		s.replayBytes -= len(s.replay[0].data)
		s.replay = s.replay[1:]
	}
	peer := s.active
	if peer != nil && !peer.enqueue(message{Version: protocolVersion, Type: "output", RunID: s.metadata.RunID, Stream: stream, Data: chunk.data, Cursor: chunk.cursor}) {
		s.active = nil
		s.attached = false
	}
	s.mu.Unlock()
}

// Complete sends the authoritative final output cursor after every producer
// has drained. Clients do not infer completion from transport EOF.
func (s *Server) Complete(result string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	peer, cursor := s.active, s.cursor
	s.mu.Unlock()
	if peer != nil {
		_ = peer.enqueueAndWait(message{Version: protocolVersion, Type: "completed", RunID: s.metadata.RunID, Cursor: cursor, Result: result})
	}
}

// Close stops all listener and connection goroutines, unblocks a waiting
// human gate, and removes only this process's session metadata.
func (s *Server) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	connections := make([]*connection, 0, len(s.connections))
	for conn := range s.connections {
		connections = append(connections, conn)
	}
	gate := s.inputGate
	s.inputGate = nil
	s.mu.Unlock()
	var closeErr error
	closeErr = errors.Join(closeErr, s.listener.Close())
	if gate != nil {
		close(gate.done)
	}
	for _, conn := range connections {
		conn.abort()
	}
	s.wg.Wait()
	metadataPath, socketPath, err := paths(s.repo, s.metadata.Workflow)
	if err != nil {
		return errors.Join(closeErr, err)
	}
	if current, err := readMetadata(metadataPath); err == nil && sameMetadata(current, s.metadata) {
		closeErr = errors.Join(closeErr, os.Remove(metadataPath))
	}
	// The socket name is deterministic and only the server that successfully
	// listened on it can have reached this point under the owner lease.
	if err := os.Remove(socketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		closeErr = errors.Join(closeErr, err)
	}
	return closeErr
}

// Attach dials a verified live endpoint and reserves the sole attachment.
// The returned connection owns the session until Close; input and signals use
// SendInput and SendSignal and are never persisted locally.
func Attach(ctx context.Context, repo gitstate.Repo, workflow string, replayLimit ...int) (*Client, error) {
	metadataPath, _, err := paths(repo, workflow)
	if err != nil {
		return nil, err
	}
	metadata, err := readMetadata(metadataPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("no live supervised session for workflow %q", workflow)
		}
		return nil, err
	}
	if err := validateMetadata(repo, workflow, metadata); err != nil {
		return nil, err
	}
	dialer := net.Dialer{}
	conn, err := dialer.DialContext(ctx, "unix", metadata.Socket)
	if err != nil {
		return nil, fmt.Errorf("connect to supervised session: %w", err)
	}
	client := &Client{conn: conn, metadata: metadata, decoder: json.NewDecoder(bufio.NewReader(conn)), encoder: json.NewEncoder(conn)}
	replay := 200
	if len(replayLimit) != 0 {
		replay = replayLimit[0]
	}
	if replay < 0 || replay > 1000 {
		_ = conn.Close()
		return nil, fmt.Errorf("invalid supervised replay limit")
	}
	if err := client.encoder.Encode(message{Version: protocolVersion, Type: "attach", RunID: metadata.RunID, Replay: replay}); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("identify supervised session: %w", err)
	}
	var response message
	if err := client.decoder.Decode(&response); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("verify supervised session: %w", err)
	}
	if response.Type != "attached" || response.RunID != metadata.RunID {
		_ = conn.Close()
		if response.Error == "" {
			response.Error = "session rejected attachment"
		}
		return nil, fmt.Errorf("attach to supervised session: %s", response.Error)
	}
	return client, nil
}

// Client is an exclusive attached terminal control connection.
type Client struct {
	conn     net.Conn
	metadata Metadata
	decoder  *json.Decoder
	encoder  *json.Encoder
	mu       sync.Mutex
}

// Frame is one authenticated server-to-terminal message.
type Frame struct {
	Output    []byte
	Stream    string
	Cursor    uint64
	Completed bool
	Detached  bool
	Result    string
}

// RunID returns the identity that was verified before attachment.
func (c *Client) RunID() string { return c.metadata.RunID }

// SendInput forwards a bounded operator response only when the live engine
// permits human-gate input.
func (c *Client) SendInput(data string) error {
	return c.send(message{Version: protocolVersion, Type: "input", RunID: c.metadata.RunID, Data: data})
}

// SendSignal forwards only the supported interruption vocabulary.
func (c *Client) SendSignal(signal string) error {
	return c.send(message{Version: protocolVersion, Type: "signal", RunID: c.metadata.RunID, Signal: signal})
}

// Detach releases terminal ownership without interrupting workflow execution.
func (c *Client) Detach() error {
	if err := c.RequestDetach(); err != nil {
		return errors.Join(err, c.Close())
	}
	for {
		frame, err := c.Receive()
		if err != nil {
			return errors.Join(err, c.Close())
		}
		if frame.Detached {
			return c.Close()
		}
	}
}

// RequestDetach asks the supervisor to reclaim terminal ownership. Callers
// with an active Receive loop must wait for its detached frame before closing.
func (c *Client) RequestDetach() error {
	return c.send(message{Version: protocolVersion, Type: "detach", RunID: c.metadata.RunID})
}

func (c *Client) send(request message) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.encoder.Encode(request); err != nil {
		return err
	}
	return nil
}

// Close releases the connection. It never signals the workflow.
func (c *Client) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

// Wait blocks until the live server closes the attachment. A clean close
// means the supervised run ended or the terminal was explicitly detached.
func (c *Client) Receive() (Frame, error) {
	if c == nil || c.decoder == nil {
		return Frame{}, io.EOF
	}
	var response message
	if err := c.decoder.Decode(&response); err != nil {
		return Frame{}, err
	}
	if response.Version != protocolVersion || response.RunID != c.metadata.RunID {
		return Frame{}, fmt.Errorf("supervised session sent a mismatched frame")
	}
	switch response.Type {
	case "output":
		if response.Stream != "stdout" && response.Stream != "stderr" {
			return Frame{}, fmt.Errorf("supervised session sent an invalid output stream")
		}
		return Frame{Output: []byte(response.Data), Stream: response.Stream, Cursor: response.Cursor}, nil
	case "completed":
		return Frame{Completed: true, Cursor: response.Cursor, Result: response.Result}, nil
	case "detached":
		return Frame{Detached: true, Cursor: response.Cursor}, nil
	default:
		return Frame{}, fmt.Errorf("supervised session sent an unexpected frame %q", response.Type)
	}
}

func paths(repo gitstate.Repo, workflow string) (string, string, error) {
	if workflow == "" {
		return "", "", fmt.Errorf("workflow name is required")
	}
	directory, err := repo.GitPath(filepath.Join("agentflow", "sessions"))
	if err != nil {
		return "", "", err
	}
	digest := sha256.Sum256([]byte(directory + "\x00" + workflow))
	key := fmt.Sprintf("%x", digest[:16])
	metadata := filepath.Join(directory, key+".json")
	socket, err := sessionEndpointPath(key)
	if err != nil {
		return "", "", err
	}
	return metadata, socket, nil
}

func prepareDirectory(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return fmt.Errorf("create supervised session directory: %w", err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect supervised session directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("supervised session directory is unsafe")
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return fmt.Errorf("restrict supervised session directory: %w", err)
	}
	return nil
}

func writeMetadata(path string, metadata Metadata) error {
	b, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create supervised session metadata: %w", err)
	}
	defer file.Close()
	if _, err := file.Write(b); err != nil {
		return fmt.Errorf("write supervised session metadata: %w", err)
	}
	return file.Sync()
}

func readMetadata(path string) (Metadata, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return Metadata{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 || !info.Mode().IsRegular() {
		return Metadata{}, fmt.Errorf("supervised session metadata has unsafe permissions")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return Metadata{}, err
	}
	if len(b) == 0 || len(b) > 4096 {
		return Metadata{}, fmt.Errorf("supervised session metadata has invalid size")
	}
	var metadata Metadata
	if err := json.Unmarshal(b, &metadata); err != nil {
		return Metadata{}, fmt.Errorf("decode supervised session metadata: %w", err)
	}
	return metadata, nil
}

func validateMetadata(repo gitstate.Repo, workflow string, metadata Metadata) error {
	_, expectedSocket, err := paths(repo, workflow)
	if err != nil {
		return err
	}
	if metadata.Version != metadataVersion || metadata.Workflow != workflow || !validRunID(metadata.RunID) || metadata.Process == nil || metadata.Socket != expectedSocket {
		return fmt.Errorf("supervised session metadata is malformed")
	}
	liveness, verified := gitstate.ProcessLiveness(metadata.Process)
	if !verified || liveness != "running" {
		return fmt.Errorf("supervised session process identity is stale or cannot be verified")
	}
	store := gitstate.NewStore(repo, workflow)
	var identity struct {
		Version int    `json:"version"`
		RunID   string `json:"run_id"`
	}
	ok, err := store.GetJSON("run-identity", &identity)
	if err != nil || !ok || identity.Version != 2 || identity.RunID != metadata.RunID {
		return fmt.Errorf("supervised session run identity does not match durable workflow state")
	}
	var descriptor gitstate.Descriptor
	ok, err = store.GetJSON(gitstate.DescriptorRecord, &descriptor)
	if err != nil || !ok || descriptor.Process == nil || descriptor.Workflow != workflow || descriptor.Process.PID != metadata.Process.PID || descriptor.Process.Start != metadata.Process.Start {
		return fmt.Errorf("supervised session process does not match observability state")
	}
	return nil
}

func removeVerifiedStale(metadataPath, socketPath string) error {
	metadata, err := readMetadata(metadataPath)
	if errors.Is(err, os.ErrNotExist) {
		return removeStaleSocket(socketPath)
	}
	if err != nil {
		return fmt.Errorf("cannot inspect existing supervised session: %w", err)
	}
	liveness, verified := gitstate.ProcessLiveness(metadata.Process)
	if !verified {
		return fmt.Errorf("cannot verify existing supervised session owner")
	}
	if liveness == "running" {
		return fmt.Errorf("a supervised session is already live for workflow %q", metadata.Workflow)
	}
	if err := os.Remove(metadataPath); err != nil {
		return fmt.Errorf("remove stale supervised session metadata: %w", err)
	}
	return removeStaleSocket(socketPath)
}

func removeStaleSocket(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect stale supervised session endpoint: %w", err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("supervised session endpoint is unsafe")
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove stale supervised session endpoint: %w", err)
	}
	return nil
}

func sameMetadata(a, b Metadata) bool {
	return a.Version == b.Version && a.Workflow == b.Workflow && a.RunID == b.RunID &&
		a.Socket == b.Socket && a.Process != nil && b.Process != nil &&
		a.Process.PID == b.Process.PID && a.Process.Start == b.Process.Start
}

func validRunID(value string) bool {
	if len(value) != len("run_")+32 || !strings.HasPrefix(value, "run_") {
		return false
	}
	for _, r := range value[len("run_"):] {
		if !(r >= '0' && r <= '9') && !(r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}
