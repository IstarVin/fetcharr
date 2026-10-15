package ws

import (
	"context"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/istarvin/fetcharr/server/internal/api"
)

// client is one connection.
//
// Three goroutines serve it besides the handler goroutine: reader (discards
// everything the peer sends and detects disconnects; it also processes pongs),
// writer (drains send) and pinger. The first one to fail calls fail, which
// makes serve close the connection.
type client struct {
	hub    *Hub
	conn   *websocket.Conn
	remote string

	send chan []byte // queued broadcast messages

	ctx    context.Context //nolint:containedctx // lifetime of one connection
	cancel context.CancelFunc

	quit     chan struct{} // closed by the first fail
	failOnce sync.Once
	mu       sync.Mutex
	code     websocket.StatusCode // 0: no close handshake, just drop the connection
	text     string
	err      error
}

func newClient(h *Hub, conn *websocket.Conn, remote string) *client {
	ctx, cancel := context.WithCancel(context.Background())
	return &client{
		hub: h, conn: conn, remote: remote,
		send: make(chan []byte, h.opts.SendBuffer),
		ctx:  ctx, cancel: cancel,
		quit: make(chan struct{}),
	}
}

// fail ends the client; only the first call counts. A non-zero code makes
// serve perform a close handshake with that code, otherwise the connection is
// dropped. It never blocks.
func (c *client) fail(code websocket.StatusCode, text string, err error) {
	c.failOnce.Do(func() {
		c.mu.Lock()
		c.code, c.text, c.err = code, text, err
		c.mu.Unlock()
		close(c.quit)
	})
}

func (c *client) reason() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case c.err != nil:
		return c.err.Error()
	case c.text != "":
		return c.text
	}
	return "closed"
}

// serve sends the snapshot, then runs until the client fails or disconnects.
func (c *client) serve() {
	defer c.cancel()
	if err := c.sendSnapshot(); err != nil {
		c.fail(websocket.StatusInternalError, "snapshot failed", err)
	}

	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); c.readLoop() }()
	go func() { defer wg.Done(); c.writeLoop() }()
	go func() { defer wg.Done(); c.pingLoop() }()

	<-c.quit
	c.mu.Lock()
	code, text := c.code, c.text
	c.mu.Unlock()
	if code != 0 {
		_ = c.conn.Close(code, text) // sends the close frame, waits briefly for the peer's
	}
	_ = c.conn.CloseNow()
	c.cancel()
	wg.Wait()
}

func (c *client) sendSnapshot() error {
	ctx, cancel := context.WithTimeout(c.ctx, c.hub.opts.WriteTimeout)
	defer cancel()
	js, err := c.hub.opts.Snapshot(ctx)
	if err != nil {
		return err
	}
	return c.conn.Write(ctx, websocket.MessageText,
		encode(snapshotMsg{Type: "snapshot", Jobs: api.NewJobDTOs(js)}))
}

// readLoop ignores everything the peer sends (spec §7). Reading is also what
// lets the library answer pings and deliver pongs and close frames.
func (c *client) readLoop() {
	for {
		if _, _, err := c.conn.Read(c.ctx); err != nil {
			c.fail(0, "client disconnected", err)
			return
		}
	}
}

func (c *client) writeLoop() {
	for {
		select {
		case <-c.quit:
			return
		case msg := <-c.send:
			ctx, cancel := context.WithTimeout(c.ctx, c.hub.opts.WriteTimeout)
			err := c.conn.Write(ctx, websocket.MessageText, msg)
			cancel()
			if err != nil {
				c.fail(0, "write failed", err)
				return
			}
		}
	}
}

func (c *client) pingLoop() {
	t := time.NewTicker(c.hub.opts.PingInterval)
	defer t.Stop()
	for {
		select {
		case <-c.quit:
			return
		case <-t.C:
			ctx, cancel := context.WithTimeout(c.ctx, c.hub.opts.PongTimeout)
			err := c.conn.Ping(ctx)
			cancel()
			if err != nil {
				c.fail(0, "ping timeout", err)
				return
			}
		}
	}
}
