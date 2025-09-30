package wsx

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	defaultWriteWait  = 10 * time.Second
	defaultMaxMsgSize = 2 << 20 // 2MB
	sendBufferSize    = 1024
)

var (
	ErrClosed = errors.New("closed")
)

type MessageHandler func(context.Context, Envelope)

type Connection struct {
	ws       *websocket.Conn
	codec    Codec
	sendCh   chan []byte
	handlers map[string]MessageHandler
	mu       sync.RWMutex

	ctx    context.Context
	cancel context.CancelFunc

	closed      chan struct{}
	closeOnce   sync.Once
	connectOnce sync.Once

	onClose           []func()
	onConnect         []func(*Connection)
	onError           []func(error)
	onMessageFallback []func(context.Context, Envelope)
	onSend            []func(context.Context, Envelope)

	id string
}

func NewConnection(ws *websocket.Conn) *Connection {
	ctx, cancel := context.WithCancel(context.Background())
	ws.SetReadLimit(defaultMaxMsgSize)
	return &Connection{
		ws:       ws,
		codec:    JSONCodec{},
		sendCh:   make(chan []byte, sendBufferSize),
		handlers: make(map[string]MessageHandler),
		ctx:      ctx,
		cancel:   cancel,
		closed:   make(chan struct{}),
	}
}

func (c *Connection) Context() context.Context {
	return c.ctx
}

func (c *Connection) ID() string {
	return c.id
}

func (c *Connection) SetCodec(codec Codec) {
	if codec == nil {
		return
	}
	c.codec = codec
}

func (c *Connection) Start(ctx context.Context) {
	go c.writeLoop()
	go c.readLoop(ctx)
	c.fireOnConnect()
}

func (c *Connection) RegisterHandler(msgType string, h MessageHandler) {
	if h == nil {
		return
	}
	c.mu.Lock()
	c.handlers[msgType] = h
	c.mu.Unlock()
}

func (c *Connection) SendBytes(b []byte) error {
	select {
	case <-c.closed:
		err := ErrClosed
		c.fireOnError(err)
		return err
	case c.sendCh <- append([]byte(nil), b...):
		return nil
	}
}

func (c *Connection) SendEnvelope(env Envelope) error {
	frame, err := c.codec.Encode(env)
	if err != nil {
		c.fireOnError(err)
		return err
	}
	if err := c.SendBytes(frame); err != nil {
		return err
	}
	c.fireOnSend(c.ctx, env)
	return nil
}

func (c *Connection) Close() {
	c.closeOnce.Do(func() {
		c.cancel()
		close(c.closed)
		close(c.sendCh)
		deadline := time.Now().Add(defaultWriteWait)
		_ = c.ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), deadline)
		_ = c.ws.Close()
		c.fireOnClose()
	})
}

func (c *Connection) OnClose(fn func()) {
	if fn == nil {
		return
	}
	c.mu.Lock()
	c.onClose = append(c.onClose, fn)
	c.mu.Unlock()
}

func (c *Connection) OnConnect(fn func(*Connection)) {
	if fn == nil {
		return
	}
	c.mu.Lock()
	c.onConnect = append(c.onConnect, fn)
	c.mu.Unlock()
}

func (c *Connection) OnError(fn func(error)) {
	if fn == nil {
		return
	}
	c.mu.Lock()
	c.onError = append(c.onError, fn)
	c.mu.Unlock()
}

func (c *Connection) OnMessageFallback(fn func(context.Context, Envelope)) {
	if fn == nil {
		return
	}
	c.mu.Lock()
	c.onMessageFallback = append(c.onMessageFallback, fn)
	c.mu.Unlock()
}

func (c *Connection) OnSend(fn func(context.Context, Envelope)) {
	if fn == nil {
		return
	}
	c.mu.Lock()
	c.onSend = append(c.onSend, fn)
	c.mu.Unlock()
}

func (c *Connection) fireOnClose() {
	c.mu.Lock()
	callbacks := c.onClose
	c.onClose = nil
	c.mu.Unlock()
	for _, fn := range callbacks {
		fn()
	}
}

func (c *Connection) fireOnConnect() {
	c.connectOnce.Do(func() {
		c.mu.Lock()
		callbacks := c.onConnect
		c.onConnect = nil
		c.mu.Unlock()
		for _, fn := range callbacks {
			fn(c)
		}
	})
}

func (c *Connection) fireOnError(err error) {
	if err == nil {
		return
	}
	c.mu.RLock()
	callbacks := append([]func(error){}, c.onError...)
	c.mu.RUnlock()
	for _, fn := range callbacks {
		fn(err)
	}
}

func (c *Connection) fireOnMessageFallback(ctx context.Context, env Envelope) {
	c.mu.RLock()
	callbacks := append([]func(context.Context, Envelope){}, c.onMessageFallback...)
	c.mu.RUnlock()
	for _, fn := range callbacks {
		fn(ctx, env)
	}
}

func (c *Connection) fireOnSend(ctx context.Context, env Envelope) {
	c.mu.RLock()
	callbacks := append([]func(context.Context, Envelope){}, c.onSend...)
	c.mu.RUnlock()
	for _, fn := range callbacks {
		fn(ctx, env)
	}
}

func (c *Connection) readLoop(ctx context.Context) {
	defer c.Close()

	for {
		select {
		case <-ctx.Done():
			return
		case <-c.closed:
			return
		default:
		}

		typeCode, frame, err := c.ws.ReadMessage()
		if err != nil {
			c.fireOnError(err)
			return
		}
		if typeCode != websocket.TextMessage && typeCode != websocket.BinaryMessage {
			continue
		}

		env, err := c.codec.Decode(frame)
		if err != nil {
			c.fireOnError(err)
			continue
		}
		h := c.getHandler(env.Type)
		if h == nil {
			c.fireOnMessageFallback(c.ctx, env)
			continue
		}
		h(c.ctx, env)
	}
}

func (c *Connection) writeLoop() {
	defer c.Close()

	for {
		select {
		case <-c.closed:
			return
		case frame, ok := <-c.sendCh:
			if !ok {
				return
			}
			c.ws.SetWriteDeadline(time.Now().Add(defaultWriteWait))
			if err := c.ws.WriteMessage(websocket.TextMessage, frame); err != nil {
				c.fireOnError(err)
				return
			}
		}
	}
}

func (c *Connection) getHandler(msgType string) MessageHandler {
	c.mu.RLock()
	h := c.handlers[msgType]
	c.mu.RUnlock()
	return h
}
