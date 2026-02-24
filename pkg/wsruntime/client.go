package wsruntime

import (
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/spf13/viper"
	"go.uber.org/zap"
)

// Client is the minimal interface implemented by every connected WebSocket client.
type Client interface {
	ID() uuid.UUID
	LastActivity() time.Time
	// SendPacket sends a packet; the concrete Packet type is from the calling package.
	// Implementations must accept any value that can be marshalled to JSON.
	SendPacket(packet Packet)
	Close()
}

// ClientConfig holds configuration for client behaviour.
type ClientConfig struct {
	PongWait                   time.Duration
	PingPeriod                 time.Duration
	SendChannelBufferSize      int
	MaxConsecutiveSendFailures int
}

// NewClientConfig reads WebSocket client configuration from Viper.
func NewClientConfig() ClientConfig {
	viper.SetDefault("websocket.client.pong_wait", 60)
	viper.SetDefault("websocket.client.ping_period", 54)
	viper.SetDefault("websocket.client.send_channel_buffer_size", 64)
	viper.SetDefault("websocket.client.max_consecutive_send_failures", 10)

	return ClientConfig{
		PongWait:                   viper.GetDuration("websocket.client.pong_wait") * time.Second,
		PingPeriod:                 viper.GetDuration("websocket.client.ping_period") * time.Second,
		SendChannelBufferSize:      viper.GetInt("websocket.client.send_channel_buffer_size"),
		MaxConsecutiveSendFailures: viper.GetInt("websocket.client.max_consecutive_send_failures"),
	}
}

// BaseClient is the concrete WebSocket client used by setruntime and searchruntime.
// The register/unregister channels are passed explicitly so this struct is not
// coupled to any particular runtime type.
type BaseClient struct {
	UserID uuid.UUID
	Send   chan []byte
	Conn   *websocket.Conn
	Done   chan struct{}

	// sendToRuntime forwards this client instance to the runtime's register channel.
	// The channel accepts *BaseClient; concrete wrappers convert after receiving.
	unregisterCh chan<- uuid.UUID
	onRegister   func(*BaseClient) // called once on startup to register with the runtime

	LastAct                 time.Time
	ConsecutiveSendFailures int
	Mutex                   sync.RWMutex

	Config ClientConfig
}

// NewBaseClient constructs and starts a BaseClient.
//   - onRegister:   called synchronously to push the new client into the runtime's register channel.
//   - unregisterCh: the runtime's channel that receives the client ID on disconnect.
func NewBaseClient(
	onRegister func(*BaseClient),
	unregisterCh chan<- uuid.UUID,
	conn *websocket.Conn,
	userID uuid.UUID,
) *BaseClient {
	cfg := NewClientConfig()
	c := &BaseClient{
		Send:         make(chan []byte, cfg.SendChannelBufferSize),
		Done:         make(chan struct{}),
		Conn:         conn,
		unregisterCh: unregisterCh,
		onRegister:   onRegister,
		UserID:       userID,
		LastAct:      time.Now(),
		Mutex:        sync.RWMutex{},
		Config:       cfg,
	}

	onRegister(c)

	go c.StartReadPolling()
	go c.StartWritePolling(cfg.PingPeriod)

	return c
}

// StartReadPolling reads from the WebSocket connection until it closes.
func (c *BaseClient) StartReadPolling() {
	defer func() {
		if err := c.Conn.Close(); err != nil {
			zap.L().Error("Error closing WS connection", zap.Error(err))
		}
		c.unregisterCh <- c.ID()
		select {
		case c.Done <- struct{}{}:
		default:
		}
		c.Close()
	}()

	_ = c.Conn.SetReadDeadline(time.Now().Add(c.Config.PongWait))
	c.Conn.SetPongHandler(func(string) error {
		return c.Conn.SetReadDeadline(time.Now().Add(c.Config.PongWait))
	})

	for {
		_, _, err := c.Conn.ReadMessage()
		if err != nil {
			if websocket.IsCloseError(err,
				websocket.CloseNormalClosure,
				websocket.CloseGoingAway,
				websocket.CloseNoStatusReceived,
			) {
				return
			}
			if websocket.IsUnexpectedCloseError(err,
				websocket.CloseGoingAway,
				websocket.CloseAbnormalClosure,
			) {
				zap.L().Error("WS unexpected close error", zap.Error(err))
				return
			}
			zap.L().Error("WS read error", zap.Error(err))
			return
		}

		c.Mutex.Lock()
		c.LastAct = time.Now()
		c.Mutex.Unlock()
	}
}

// StartWritePolling writes messages and sends pings on a ticker.
func (c *BaseClient) StartWritePolling(pingPeriod time.Duration) {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		_ = c.Conn.SetReadDeadline(time.Now().Add(time.Second))
	}()

	for {
		select {
		case message := <-c.Send:
			if message == nil {
				_ = c.Conn.WriteMessage(
					websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseNormalClosure, "closing connection"),
				)
				return
			}
			if err := c.Conn.WriteMessage(websocket.TextMessage, message); err != nil {
				zap.L().Error("WS write error", zap.Error(err))
				return
			}
		case <-ticker.C:
			if err := c.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				zap.L().Error("WS ping error", zap.Error(err))
				return
			}
		case <-c.Done:
			return
		}
	}
}

// ID implements Client.
func (c *BaseClient) ID() uuid.UUID { return c.UserID }

// LastActivity implements Client.
func (c *BaseClient) LastActivity() time.Time {
	c.Mutex.RLock()
	defer c.Mutex.RUnlock()
	return c.LastAct
}

// SendPacket implements Client using the shared send channel.
func (c *BaseClient) SendPacket(p Packet) {
	data, err := p.ToJSON()
	if err != nil {
		zap.L().Error("Error marshalling WS packet", zap.Error(err))
		return
	}

	select {
	case c.Send <- data:
		c.Mutex.Lock()
		c.ConsecutiveSendFailures = 0
		c.Mutex.Unlock()
	default:
		c.Mutex.Lock()
		c.ConsecutiveSendFailures++
		failures := c.ConsecutiveSendFailures
		c.Mutex.Unlock()

		zap.L().Warn("WS send channel blocked, dropping packet",
			zap.String("client_id", c.UserID.String()),
			zap.Int("consecutive_failures", failures))

		if failures >= c.Config.MaxConsecutiveSendFailures {
			zap.L().Warn("Too many WS send failures, closing client",
				zap.String("client_id", c.UserID.String()))
			c.Close()
		}
	}
}

// Close initiates a graceful shutdown of the client.
func (c *BaseClient) Close() {
	select {
	case c.Send <- nil:
	default:
	}

	time.AfterFunc(2*time.Second, func() {
		select {
		case c.Done <- struct{}{}:
		default:
		}
	})
}
