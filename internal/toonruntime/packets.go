package toonruntime

import (
	"encoding/json"

	"github.com/Zapharaos/offtoon-backend/internal/toon"
	"github.com/Zapharaos/offtoon-backend/pkg/wsruntime"
)

type PacketType string

const (
	PacketTypeInit      PacketType = "init"
	PacketTypeFatal     PacketType = "fatal"
	PacketTypeProgress  PacketType = "progress"
	PacketTypeCompleted PacketType = "completed"
)

// packetSpec is a struct that contains all the possible packets
// WARNING : used for swagger doc and generation
type packetSpec struct {
	Packet          packet          `json:"packet"`
	PacketInit      PacketInit      `json:"packetInit"`
	PacketFatal     PacketFatal     `json:"packetFatal"`
	PacketProgress  PacketProgress  `json:"packetProgress"`
	PacketCompleted PacketCompleted `json:"packetCompleted"`
}

// Packet interface
type Packet interface {
	ToJSON() ([]byte, error)
}

// Packet is a generic packet
type packet struct {
	Type PacketType `json:"type"`
	Hash string     `json:"hash"`
}

// ToJSON returns the JSON representation of the packet
func (p *packet) ToJSON() ([]byte, error) {
	return json.Marshal(p)
}

// --------------------------------------------
// --------------------------------------------
// --------------------------------------------

// PacketInit is a packet to initialize the set
type PacketInit struct {
	packet
}

func NewPacketInit() *PacketInit {
	return &PacketInit{
		packet: packet{
			Type: PacketTypeInit,
		},
	}
}

func (p *PacketInit) ToJSON() ([]byte, error) {
	return json.Marshal(p)
}

// PacketFatal is a packet to send a fatal internal error
type PacketFatal struct {
	packet
	Step    toon.FetchErrorStep `json:"step"`
	Message string              `json:"message"`
}

// NewPacketFatal creates a new PacketFatal
func NewPacketFatal(step toon.FetchErrorStep, message string) *PacketFatal {
	return &PacketFatal{
		packet: packet{
			Type: PacketTypeFatal,
		},
		Step:    step,
		Message: message,
	}
}

// ToJSON returns the JSON representation of the packet
func (p *PacketFatal) ToJSON() ([]byte, error) {
	return json.Marshal(p)
}

// --------------------------------------------
// --------------------------------------------
// --------------------------------------------

// PacketProgress is a packet sent to report download progress to connected clients.
type PacketProgress struct {
	packet
	Total int   `json:"total"` // Total chapters requested
	Done  int   `json:"done"`  // Chapters fully downloaded so far
	Items []any `json:"items"` // Chapters downloaded in this batch
}

// NewPacketProgress creates a new PacketProgress from a wsruntime.Progress snapshot.
func NewPacketProgress(p wsruntime.Progress) *PacketProgress {
	return &PacketProgress{
		packet: packet{Type: PacketTypeProgress},
		Total:  p.Total,
		Done:   p.Done,
		Items:  p.Items,
	}
}

// ToJSON returns the JSON representation of the packet.
func (p *PacketProgress) ToJSON() ([]byte, error) {
	return json.Marshal(p)
}

// --------------------------------------------
// --------------------------------------------
// --------------------------------------------

// PacketCompleted signals that a download job has finished successfully.
type PacketCompleted struct {
	packet
	Total int `json:"total"` // Total chapters that were downloaded
}

// NewPacketCompleted creates a new PacketCompleted.
func NewPacketCompleted(total int) *PacketCompleted {
	return &PacketCompleted{
		packet: packet{Type: PacketTypeCompleted},
		Total:  total,
	}
}

// ToJSON returns the JSON representation of the packet.
func (p *PacketCompleted) ToJSON() ([]byte, error) {
	return json.Marshal(p)
}
