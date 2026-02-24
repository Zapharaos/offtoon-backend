package toonruntime

import (
	"sync"

	"github.com/google/uuid"
)

// clientHolder holds clients using the setruntime-local Client interface.
// It replicates the wsruntime.ClientHolder pattern without embedding it, because
// the local Client interface has close() (unexported) rather than Close() (exported).
type clientHolder struct {
	clients     map[uuid.UUID]Client
	clientMutex sync.RWMutex
	needsMutex  bool
}

func newClientHolder(needsMutex bool) *clientHolder {
	return &clientHolder{
		clients:    make(map[uuid.UUID]Client),
		needsMutex: needsMutex,
	}
}

func (h *clientHolder) broadcastPacket(p Packet) {
	if h.needsMutex {
		h.clientMutex.RLock()
		defer h.clientMutex.RUnlock()
	}
	for _, c := range h.clients {
		c.SendPacket(p)
	}
}

func (h *clientHolder) registerClient(c Client) {
	if h.needsMutex {
		h.clientMutex.Lock()
		defer h.clientMutex.Unlock()
	}
	h.clients[c.ID()] = c
}

func (h *clientHolder) unregisterClient(id uuid.UUID) {
	if h.needsMutex {
		h.clientMutex.Lock()
		defer h.clientMutex.Unlock()
	}
	delete(h.clients, id)
}
