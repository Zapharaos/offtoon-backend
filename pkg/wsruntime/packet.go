package wsruntime

// Packet is the common interface implemented by every WebSocket packet type.
type Packet interface {
	ToJSON() ([]byte, error)
}
