package toonruntime

import (
	"github.com/Zapharaos/offtoon-backend/internal/toon"
	"github.com/Zapharaos/offtoon-backend/pkg/wsruntime"
	"github.com/google/uuid"
)

type DataType uint8
type DataChangeReason uint8

// TODO : add/remove datatypes + handle them
const (
	DataTypeToon DataType = iota
	DataTypeChapter
)

const (
	DataTypeCreated DataChangeReason = iota
	DataTypeUpdated
	DataTypeCompleted
	DataTypeFailed
	DataTypeProgress
)

type dataChange struct {
	Id       uuid.UUID
	Type     DataType
	Reason   DataChangeReason
	Progress wsruntime.Progress // Only used when working with batches
}

// handleDataChangeCreated handles the creation of data
func (rt *RuntimeToon) handleDataChangeCreated(change dataChange) {
	// TODO
	switch change.Type {
	case DataTypeToon:
		rt.broadcastPacket(NewPacketInit())
		break
	default:
		break
	}
}

// handleDataChangeUpdated handles the update of data
func (rt *RuntimeToon) handleDataChangeUpdated(change dataChange) {
	// TODO
	switch change.Type {
	case DataTypeToon:
		rt.broadcastPacket(NewPacketInit())
		break
	default:
		break
	}
}

// handleDataChangeCompleted handles the data completion
func (rt *RuntimeToon) handleDataChangeCompleted(change dataChange) {
	// TODO
	switch change.Type {
	case DataTypeToon:
		rt.broadcastPacket(NewPacketInit())
		break
	default:
		break
	}
}

// handleDataChangeFailed handles the data failure
func (rt *RuntimeToon) handleDataChangeFailed(change dataChange) {
	// Determine the fatal error code and message based on the data type
	var fatalPacket *PacketFatal

	fetchError := rt.fetchError
	if fetchError != nil {
		fatalPacket = NewPacketFatal(fetchError.Step, fetchError.Message)
	} else {
		// Fallback if FetchError is not set for some reason
		fatalPacket = NewPacketFatal(toon.FetchErrorUnknown, "An unknown error occurred during set processing")
	}

	// Broadcast the fatal error to all connected clients
	rt.broadcastPacket(fatalPacket)
}

// handleDataChangeProgress handles batch progress updates
func (rt *RuntimeToon) handleDataChangeProgress(change dataChange) {
	// TODO
	switch change.Type {
	case DataTypeToon:
		rt.broadcastPacket(NewPacketInit())
		break
	default:
		// Unknown data type for progress, ignore
		return
	}
}
