package toonruntime

import (
	"time"

	"github.com/Zapharaos/offtoon-backend/pkg/wsruntime"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

// Client is the setruntime-local interface; close() is unexported intentionally.
type Client interface {
	ID() uuid.UUID
	LastActivity() time.Time
	SendPacket(packet Packet)
	close()
}

// client wraps wsruntime.BaseClient and bridges the unexported close() method.
type client struct {
	*wsruntime.BaseClient
}

// NewClient creates and starts a new WebSocket client, registering it with the runtime set.
func NewClient(rs *RuntimeToon, conn *websocket.Conn, userId uuid.UUID) Client {
	// onRegister pushes the base client into the runtime's typed register channel as a Client.
	onRegister := func(bc *wsruntime.BaseClient) {
		c := &client{BaseClient: bc}
		rs.register <- c
	}
	base := wsruntime.NewBaseClient(onRegister, rs.unregisterUUID(), conn, userId)
	return &client{BaseClient: base}
}

// SendPacket delegates to the shared implementation, bridging the local Packet interface.
func (c *client) SendPacket(p Packet) {
	c.BaseClient.SendPacket(p)
}

// close delegates to the exported Close on BaseClient.
func (c *client) close() {
	c.BaseClient.Close()
}
