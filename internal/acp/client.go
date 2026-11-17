// Package acp connects the native conversation UI to fx's JSON-RPC stdio server.
// The agent owns tools, history and model access; this client only presents them.
package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"
)

var ErrClosed = errors.New("fx connection closed")

type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Message }

type Packet struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

// Client can have a prompt in flight while receiving permission requests.
// Its reader never waits for the UI, nor for a request it is responsible for reading.
//
// Two completion signals are kept apart on purpose (§4.3): Done reports that
// the protocol connection is unusable, so every pending RPC can give up;
// Exited reports that the direct fx child has been reaped by cmd.Wait, so the
// process (and the pipes it owned) is really gone. Closing a session waits for
// the second before a replacement is started.
type Client struct {
	mu      sync.Mutex
	writeMu sync.Mutex
	next    uint64
	pending map[string]chan Packet
	input   io.WriteCloser
	done    chan struct{}
	cancel  context.CancelFunc
	err     error
	exited  chan struct{}
	exitErr error
	onEvent func(Packet)

	doneOnce sync.Once
	exitOnce sync.Once

	writes    chan writeJob
	writeOnce sync.Once
}

// writeJob is one outbound frame. The writer pump owns the pipe: it honours
// cancellation before send, aborts in-flight blocked writes by tearing the
// connection down, and never lets unbounded goroutines pile up on the queue.
type writeJob struct {
	data    []byte
	ctx     context.Context
	done    chan error
	started chan struct{} // closed once writeMu is held and Write may block
}

const writeQueueDepth = 64

func (c *Client) startWritePump() {
	c.writeOnce.Do(func() {
		c.writes = make(chan writeJob, writeQueueDepth)
		go c.runWritePump()
	})
}

func (c *Client) runWritePump() {
	for {
		select {
		case <-c.done:
			c.drainWriteQueue()
			return
		case job, ok := <-c.writes:
			if !ok {
				return
			}
			c.performWrite(job)
		}
	}
}

func (c *Client) drainWriteQueue() {
	for {
		select {
		case job := <-c.writes:
			job.done <- ErrClosed
		default:
			return
		}
	}
}

func (c *Client) performWrite(job writeJob) {
	if err := job.ctx.Err(); err != nil {
		job.done <- err
		return
	}
	select {
	case <-c.done:
		job.done <- ErrClosed
		return
	default:
	}

	finished := make(chan error, 1)
	go func() {
		c.writeMu.Lock()
		if job.started != nil {
			close(job.started)
		}
		defer c.writeMu.Unlock()
		select {
		case <-c.done:
			finished <- ErrClosed
			return
		default:
		}
		_, err := c.input.Write(job.data)
		if err != nil {
			c.finish(err)
		}
		finished <- err
	}()

	if job.started != nil {
		<-job.started
	}

	select {
	case err := <-finished:
		job.done <- err
	case <-job.ctx.Done():
		// A partial frame may have reached the peer; the connection is dead.
		c.finish(ErrClosed)
		job.done <- job.ctx.Err()
		<-finished
	case <-c.done:
		job.done <- ErrClosed
		<-finished
	}
}

func (c *Client) enqueueWrite(ctx context.Context, data []byte, started chan struct{}) error {
	c.startWritePump()
	job := writeJob{data: data, ctx: ctx, done: make(chan error, 1), started: started}
	select {
	case c.writes <- job:
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return ErrClosed
	}
	select {
	case err := <-job.done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return ErrClosed
	}
}

func Start(ctx context.Context, executable, dir string, args []string, onEvent func(Packet)) (*Client, error) {
	return StartWithGlobals(ctx, executable, dir, nil, args, onEvent)
}

// StartWithGlobals starts `fx <globalFlags...> acp <args...>`. fx accepts a
// small set of flags only before the subcommand (notably --context-limit,
// --add-dir and --no-additional-dirs), so they cannot be appended to the acp
// argument list.
func StartWithGlobals(ctx context.Context, executable, dir string, globals, args []string, onEvent func(Packet)) (*Client, error) {
	ctx, cancel := context.WithCancel(ctx)
	argv := make([]string, 0, len(globals)+1+len(args))
	argv = append(argv, globals...)
	argv = append(argv, "acp")
	argv = append(argv, args...)
	cmd := exec.CommandContext(ctx, executable, argv...)
	cmd.Dir = dir
	input, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	// The client owns the read end of stdout. With cmd.StdoutPipe the reader
	// had to reach EOF before cmd.Wait could be called, so a direct child
	// that exited while one of its own children still held the pipe was
	// never noticed: Done stayed open and the conversation kept waiting on a
	// process that was gone. Owning the pipe lets the exit be observed on its
	// own, with a bounded drain for frames written just before it.
	output, writer, err := os.Pipe()
	if err != nil {
		input.Close()
		cancel()
		return nil, err
	}
	cmd.Stdout = writer
	// Protocol errors arrive on stdout. Diagnostic lines are not frames: a nil
	// Stderr is the null device, with no copying goroutine for Wait to join.
	cmd.Stderr = nil
	cmd.WaitDelay = outputDrain
	if err = cmd.Start(); err != nil {
		input.Close()
		output.Close()
		writer.Close()
		cancel()
		return nil, err
	}
	// The child holds its own copy; ours would keep the reader from ever
	// seeing EOF.
	writer.Close()
	c := &Client{input: input, done: make(chan struct{}), exited: make(chan struct{}), pending: make(map[string]chan Packet), cancel: cancel, onEvent: onEvent}
	c.startWritePump()
	readDone := make(chan struct{})
	go func() {
		// The protocol side ends first: a scanner error, EOF or an unparsable
		// frame means no reply can arrive, so waiters are released now.
		defer close(readDone)
		readErr := c.read(output)
		if readErr == nil {
			readErr = ErrClosed
		}
		c.finish(readErr)
	}()
	go func() {
		// The process is reaped on its own goroutine. Once it is gone the
		// reader gets outputDrain to reach EOF; a pipe still held by a
		// descendant is then closed from this side, so Done is closed before
		// Exited in every case.
		waitErr := cmd.Wait()
		drain := time.NewTimer(outputDrain)
		select {
		case <-readDone:
			drain.Stop()
		case <-drain.C:
			output.Close()
			<-readDone
		}
		output.Close()
		c.markExited(waitErr)
	}()
	return c, nil
}

// outputDrain bounds how long frames already written by an exited fx are
// still read. It is the inherited-pipe bound WaitDelay used to promise.
const outputDrain = 100 * time.Millisecond

// read decodes frames until the stream ends. It reports why it stopped; the
// caller decides what that means for the connection.
func (c *Client) read(r io.Reader) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 8192), 8*1024*1024)
	for scanner.Scan() {
		if len(scanner.Bytes()) == 0 {
			continue
		}
		var p Packet
		if err := json.Unmarshal(scanner.Bytes(), &p); err != nil || p.JSONRPC != "2.0" {
			return errors.New("fx sent an invalid ACP frame")
		}
		if p.Method != "" {
			if c.onEvent != nil {
				c.onEvent(p)
			}
			continue
		}
		c.mu.Lock()
		ch := c.pending[string(p.ID)]
		delete(c.pending, string(p.ID))
		c.mu.Unlock()
		if ch != nil {
			ch <- p
		}
	}
	return scanner.Err()
}

// finish marks the protocol connection closed. It is idempotent and never
// blocks: it only records the reason, releases every waiter and makes the
// process stop reading, so the reaping goroutine can run.
func (c *Client) finish(err error) {
	c.doneOnce.Do(func() {
		c.mu.Lock()
		c.err = err
		c.mu.Unlock()
		close(c.done)
		c.cancel()
		c.input.Close()
	})
}

// markExited records that cmd.Wait returned: the direct child is reaped.
func (c *Client) markExited(err error) {
	c.exitOnce.Do(func() {
		c.mu.Lock()
		c.exitErr = err
		c.mu.Unlock()
		close(c.exited)
	})
}

// Shutdown lets fx observe stdin EOF and cancel/reap its own tool processes.
// The polite wait is bounded by grace; if it elapses the process is killed and
// reaping is still awaited, bounded by ctx. A nil error means the process was
// confirmed reaped, not merely signalled — callers use that to decide whether
// a replacement may start (§4.3).
//
// The caller runs this off the app loop.
func (c *Client) Shutdown(ctx context.Context, grace time.Duration) error {
	c.input.Close()
	polite := time.NewTimer(grace)
	defer polite.Stop()
	select {
	case <-c.exited:
		return nil
	case <-polite.C:
	case <-ctx.Done():
		c.Close()
		return ctx.Err()
	}
	// fx did not exit on its own; terminate it and still confirm reaping.
	c.Close()
	select {
	case <-c.exited:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("fx did not exit: %w", ctx.Err())
	}
}

// Close tears the connection down. It is idempotent and does not wait: the
// reaping goroutine closes Exited afterwards.
func (c *Client) Close() { c.finish(ErrClosed) }

// Done reports that the protocol connection is unusable.
func (c *Client) Done() <-chan struct{} { return c.done }

// Exited reports that the direct fx child has been reaped by cmd.Wait. It does
// not claim anything about tools fx detached from itself.
func (c *Client) Exited() <-chan struct{} { return c.exited }

func (c *Client) Err() error { c.mu.Lock(); defer c.mu.Unlock(); return c.err }

// ExitErr is cmd.Wait's result, meaningful only after Exited is closed.
func (c *Client) ExitErr() error { c.mu.Lock(); defer c.mu.Unlock(); return c.exitErr }

func (c *Client) write(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	select {
	case <-c.done:
		return ErrClosed
	default:
	}
	return c.enqueueWrite(context.Background(), b, nil)
}

func (c *Client) Call(ctx context.Context, method string, params any, result any) error {
	c.mu.Lock()
	c.next++
	key := strconv.FormatUint(c.next, 10)
	ch := make(chan Packet, 1)
	c.pending[key] = ch
	c.mu.Unlock()
	defer func() { c.mu.Lock(); delete(c.pending, key); c.mu.Unlock() }()

	payload, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(key), "method": method, "params": params})
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	if err := c.enqueueWrite(ctx, payload, nil); err != nil {
		return err
	}
	select {
	case p := <-ch:
		if p.Error != nil {
			return p.Error
		}
		if result != nil {
			if err := json.Unmarshal(p.Result, result); err != nil {
				return fmt.Errorf("invalid %s response: %w", method, err)
			}
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return c.Err()
	}
}
func (c *Client) Notify(method string, params any) error {
	return c.write(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}
func (c *Client) Respond(id json.RawMessage, result any) error {
	return c.write(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}
func (c *Client) Reject(id json.RawMessage) error {
	return c.write(map[string]any{"jsonrpc": "2.0", "id": id, "error": Error{Code: -32601, Message: "Client capability not supported"}})
}
