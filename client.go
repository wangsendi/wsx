package wsx

import (
	"context"
	"errors"
	"math"
	"math/rand"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var (
	// ErrDialing 表示客户端正在建立连接。
	ErrDialing = errors.New("dialing")
)

// ClientOptions 控制自动重连参数。
type ClientOptions struct {
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
	BackoffFactor  float64
	JitterRatio    float64
	Dialer         *websocket.Dialer
}

func (o *ClientOptions) normalize() {
	if o.InitialBackoff <= 0 {
		o.InitialBackoff = 500 * time.Millisecond
	}
	if o.MaxBackoff <= 0 {
		o.MaxBackoff = 30 * time.Second
	}
	if o.BackoffFactor <= 1 {
		o.BackoffFactor = 1.8
	}
	if o.JitterRatio <= 0 {
		o.JitterRatio = 0.5
	}
	if o.Dialer == nil {
		o.Dialer = websocket.DefaultDialer
	}
}

// Client 提供自动重连能力的 websocket 客户端。
type Client struct {
	url string
	opt ClientOptions

	mu     sync.RWMutex
	conn   *Connection
	closed bool
}

// NewClient 创建新的 Client。
func NewClient(url string, opts ...ClientOptions) *Client {
	opt := ClientOptions{}
	if len(opts) > 0 {
		opt = opts[0]
	}
	opt.normalize()
	return &Client{url: url, opt: opt}
}

// Connect 启动自动重连循环，成功后执行 register。
func (c *Client) Connect(ctx context.Context, register func(*Connection)) error {
	if register == nil {
		register = func(*Connection) {}
	}
	backoff := c.opt.InitialBackoff
	for {
		conn, err := c.dial(ctx)
		if err == nil {
			c.setConnection(conn)
			register(conn)
			conn.Start(ctx)
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}

		backoff = c.nextBackoff(backoff)
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// Send 调用底层 Connection 发送 Envelope。
func (c *Client) Send(env Envelope) error {
	conn := c.getConnection()
	if conn == nil {
		return ErrDialing
	}
	return conn.SendEnvelope(env)
}

// Close 停止自动重连并关闭当前连接。
func (c *Client) Close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	conn := c.conn
	c.conn = nil
	c.mu.Unlock()
	if conn != nil {
		conn.Close()
	}
}

func (c *Client) dial(ctx context.Context) (*Connection, error) {
	c.mu.RLock()
	if c.closed {
		c.mu.RUnlock()
		return nil, context.Canceled
	}
	c.mu.RUnlock()

	ws, resp, err := c.opt.Dialer.DialContext(ctx, c.url, http.Header{})
	if err != nil {
		if resp != nil {
			resp.Body.Close()
		}
		return nil, err
	}

	conn := NewConnection(ws)
	conn.OnClose(func() {
		c.mu.Lock()
		if c.conn == conn {
			c.conn = nil
		}
		c.mu.Unlock()
	})

	return conn, nil
}

func (c *Client) setConnection(conn *Connection) {
	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()
}

func (c *Client) getConnection() *Connection {
	c.mu.RLock()
	conn := c.conn
	c.mu.RUnlock()
	return conn
}

func (c *Client) nextBackoff(cur time.Duration) time.Duration {
	if cur <= 0 {
		cur = c.opt.InitialBackoff
	}
	val := float64(cur) * c.opt.BackoffFactor
	if max := float64(c.opt.MaxBackoff); val > max {
		val = max
	}
	jitter := 1 + (rand.Float64()*2-1)*c.opt.JitterRatio
	return time.Duration(math.Max(float64(c.opt.InitialBackoff), val*jitter))
}
