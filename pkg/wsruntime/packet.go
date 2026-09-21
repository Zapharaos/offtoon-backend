package wsruntime

// Packet is the common interface implemented by every WebSocket packet type.
type Packet interface {
	ToJSON() ([]byte, error)
}

// Droppable is implemented by packets whose content is superseded by the next
// packet of the same kind — a progress counter, typically.
//
// It exists because the send channel is bounded. A long download emits progress
// far faster than a client consumes it, and without this distinction a full
// channel costs whichever packet happened to arrive next: losing a progress
// update is invisible, losing the completion packet strands the client forever.
// Packets that do not implement it are treated as essential and are waited on
// rather than dropped.
type Droppable interface {
	// Droppable reports whether this packet may be discarded when the send
	// channel is full.
	Droppable() bool
}

// droppable reports whether p opts in to being dropped under back-pressure.
func droppable(p Packet) bool {
	d, ok := p.(Droppable)
	return ok && d.Droppable()
}
