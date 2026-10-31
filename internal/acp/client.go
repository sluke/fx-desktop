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
type Client struct {
	mu      sync.Mutex
	writeMu sync.Mutex
	next    uint64
	pending map[string]chan Packet
	input   io.WriteCloser
	done    chan struct{}
	once    sync.Once
	cancel  context.CancelFunc
	err     error
	onEvent func(Packet)
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
	cmd.WaitDelay = 100 * time.Millisecond // bound inherited output pipes on process shutdown
	input, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		input.Close()
		cancel()
		return nil, err
	}
	// Protocol errors arrive on stdout. Do not mix diagnostic lines into frames.
	cmd.Stderr = io.Discard
	if err = cmd.Start(); err != nil {
		input.Close()
		output.Close()
		cancel()
		return nil, err
	}
	c := &Client{input: input, done: make(chan struct{}), pending: make(map[string]chan Packet), cancel: cancel, onEvent: onEvent}
	go func() {
		c.read(output)
		err := cmd.Wait()
		if err == nil {
			err = ErrClosed
		}
		c.finish(err)
	}()
	return c, nil
}

func (c *Client) read(r io.Reader) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 8192), 8*1024*1024)
	for scanner.Scan() {
		if len(scanner.Bytes()) == 0 {
			continue
		}
		var p Packet
		if err := json.Unmarshal(scanner.Bytes(), &p); err != nil || p.JSONRPC != "2.0" {
			c.finish(errors.New("fx sent an invalid ACP frame"))
			return
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
	err := scanner.Err()
	if err == nil {
		err = ErrClosed
	}
	c.finish(err)
}
func (c *Client) finish(err error) {
	c.once.Do(func() {
		c.mu.Lock()
		c.err = err
		c.mu.Unlock()
		close(c.done)
		c.cancel()
		c.input.Close()
	})
}

// Shutdown lets fx observe stdin EOF and cancel/reap its own tool processes.
// The caller runs this off the app loop; an unresponsive process is bounded.
func (c *Client) Shutdown() {
	c.input.Close()
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case <-c.done:
	case <-timer.C:
		c.Close()
	}
}
func (c *Client) Close()                { c.finish(ErrClosed) }
func (c *Client) Done() <-chan struct{} { return c.done }
func (c *Client) Err() error            { c.mu.Lock(); defer c.mu.Unlock(); return c.err }

func (c *Client) write(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	select {
	case <-c.done:
		return ErrClosed
	default:
	}
	_, err = c.input.Write(b)
	if err != nil {
		c.finish(err)
	}
	return err
}

func (c *Client) Call(ctx context.Context, method string, params any, result any) error {
	c.mu.Lock()
	c.next++
	key := strconv.FormatUint(c.next, 10)
	ch := make(chan Packet, 1)
	c.pending[key] = ch
	c.mu.Unlock()
	defer func() { c.mu.Lock(); delete(c.pending, key); c.mu.Unlock() }()
	if err := c.write(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(key), "method": method, "params": params}); err != nil {
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
