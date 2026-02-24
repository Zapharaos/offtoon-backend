package toonruntime

import (
	"encoding/json"

	"github.com/Zapharaos/offtoon-backend/internal/toon"
)

type PacketType string

const (
	PacketTypeInit  PacketType = "init"
	PacketTypeFatal PacketType = "fatal"
)

// packetSpec is a struct that contains all the possible packets
// WARNING : used for swagger doc and generation
type packetSpec struct {
	Packet      packet      `json:"packet"`
	PacketInit  PacketInit  `json:"packetInit"`
	PacketFatal PacketFatal `json:"packetFatal"`
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
