package ipc

import (
	"encoding/json"
	"errors"
	"net"
	"sync"
	"time"
)

// Client talks to an orbit supervisor over its socket.
type Client struct {
	conn net.Conn

	mu      sync.Mutex
	nextID  int
	pending map[int]chan Message
	closed  bool

	notifyMu  sync.Mutex
	onNotify  func(Message)
	closeOnce sync.Once
	onClose   []func()
	done      chan struct{}
}

// Connect dials a socket; it fails when nobody is listening.
func Connect(path string, timeout time.Duration) (*Client, error) {
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	conn, err := dial(path, timeout)
	if err != nil {
		return nil, err
	}
	c := &Client{conn: conn, nextID: 1, pending: map[int]chan Message{}, done: make(chan struct{})}
	go c.readLoop()
	return c, nil
}

func (c *Client) readLoop() {
	readMessages(c.conn, func(m Message) {
		if m.IsResponse() {
			c.mu.Lock()
			ch := c.pending[*m.ID]
			delete(c.pending, *m.ID)
			c.mu.Unlock()
			if ch != nil {
				ch <- m
			}
			return
		}
		if m.Method != "" {
			c.notifyMu.Lock()
			fn := c.onNotify
			c.notifyMu.Unlock()
			if fn != nil {
				fn(m)
			}
		}
	}, nil)
	c.shutdown()
}

func (c *Client) shutdown() {
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.closed = true
		pending := c.pending
		c.pending = map[int]chan Message{}
		c.mu.Unlock()
		for _, ch := range pending {
			close(ch)
		}
		c.conn.Close()
		close(c.done)
		c.notifyMu.Lock()
		fns := c.onClose
		c.notifyMu.Unlock()
		for _, f := range fns {
			f()
		}
	})
}

// OnNotification sets the function called with every notification (it runs on the reader goroutine, in order).
func (c *Client) OnNotification(fn func(Message)) {
	c.notifyMu.Lock()
	c.onNotify = fn
	c.notifyMu.Unlock()
}

// OnClose registers a function called once when the connection ends.
func (c *Client) OnClose(fn func()) {
	c.notifyMu.Lock()
	c.onClose = append(c.onClose, fn)
	c.notifyMu.Unlock()
}

// Closed reports whether the connection has ended.
func (c *Client) Closed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

// Done is closed when the connection ends.
func (c *Client) Done() <-chan struct{} { return c.done }

// Call sends a request and decodes its result into out (nil to ignore it).
func (c *Client) Call(method string, params any, out any) error {
	var raw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return err
		}
		raw = b
	}
	ch := make(chan Message, 1)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return errors.New("not connected to orbit")
	}
	id := c.nextID
	c.nextID++
	c.pending[id] = ch
	c.mu.Unlock()
	if _, err := c.conn.Write(encode(Message{ID: &id, Method: method, Params: raw})); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return err
	}
	m, ok := <-ch
	if !ok {
		return errors.New("connection to orbit closed")
	}
	if m.Error != nil {
		return errors.New(m.Error.Message)
	}
	if out != nil && m.Result != nil {
		return json.Unmarshal(m.Result, out)
	}
	return nil
}

// Close hangs up.
func (c *Client) Close() { c.shutdown() }
