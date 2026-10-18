// Package watest provides a fake wa.Client for tests.
package watest

import (
	"context"
	"sync"

	"github.com/wasichu/go-whassup/internal/wa"
)

// Client is a scriptable wa.Client. Configure the exported fields before use.
type Client struct {
	Paired     bool
	PairCode   string
	PairErr    error
	ConnectErr error
	// Events emitted synchronously, in order, from a successful Pair or Connect.
	PairEvents    []wa.Event
	ConnectEvents []wa.Event

	mu        sync.Mutex
	handler   func(wa.Event)
	pairPhone string
	connected bool
	closed    bool
}

var _ wa.Client = (*Client)(nil)

func (c *Client) SetEventHandler(h func(wa.Event)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.handler = h
}

// Emit delivers evt to the registered handler, as the protocol goroutines would.
func (c *Client) Emit(evt wa.Event) {
	c.mu.Lock()
	h := c.handler
	c.mu.Unlock()
	if h != nil {
		h(evt)
	}
}

func (c *Client) Pair(_ context.Context, phone string) (string, error) {
	if c.Paired {
		return "", wa.ErrAlreadyPaired
	}
	c.mu.Lock()
	c.pairPhone = phone
	c.mu.Unlock()
	if c.PairErr != nil {
		return "", c.PairErr
	}
	for _, evt := range c.PairEvents {
		c.Emit(evt)
	}
	return c.PairCode, nil
}

func (c *Client) Connect(context.Context) error {
	if !c.Paired {
		return wa.ErrNotPaired
	}
	if c.ConnectErr != nil {
		return c.ConnectErr
	}
	c.mu.Lock()
	c.connected = true
	c.mu.Unlock()
	for _, evt := range c.ConnectEvents {
		c.Emit(evt)
	}
	return nil
}

func (c *Client) IsPaired() bool { return c.Paired }

func (c *Client) Disconnect() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.connected = false
}

func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.connected, c.closed = false, true
	return nil
}

// PairPhone returns the phone number passed to Pair.
func (c *Client) PairPhone() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pairPhone
}

// Connected reports whether Connect succeeded and no Disconnect/Close followed.
func (c *Client) Connected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connected
}

// Closed reports whether Close was called.
func (c *Client) Closed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}
