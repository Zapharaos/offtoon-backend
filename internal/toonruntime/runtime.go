package toonruntime

import (
	"fmt"
	"sync"
	"time"

	"github.com/Zapharaos/offtoon-backend/internal/toon"
	"github.com/Zapharaos/offtoon-backend/pkg/supervisor"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type RuntimeToon struct {
	ID  uuid.UUID // Unique ID for this runtime (used for websocket connections)
	opt RuntimeOptions

	// Core runtime data
	t          *toon.Toon
	fetchError *toon.FetchError

	// Client management
	*clientHolder
	register   chan Client
	unregister chan uuid.UUID
	changeChan chan dataChange

	// Synchronization
	done  chan struct{}
	onEnd func(uuid.UUID)
	wg    *sync.WaitGroup

	errorLogger *supervisor.AsyncErrorLogger
}

// NewRuntimeToon creates a new runtime toon
func NewRuntimeToon(t *toon.Toon, opt RuntimeOptions, wg *sync.WaitGroup, errorLogger *supervisor.AsyncErrorLogger) *RuntimeToon {
	rt := &RuntimeToon{
		ID:           uuid.New(),
		opt:          opt,
		t:            t,
		clientHolder: newClientHolder(true),
		register:     make(chan Client, opt.ClientChanCap),
		unregister:   make(chan uuid.UUID, opt.ClientChanCap),
		changeChan:   make(chan dataChange, opt.ChangeChanCap),
		done:         make(chan struct{}),
		wg:           wg,
		errorLogger:  errorLogger,
	}

	return rt
}

// Start starts the runtime toon
func (rt *RuntimeToon) Start() {
	go rt.run()
}

// PushChange pushes a data change to the runtime toon.
// It is non-blocking: if the channel is full the change is dropped with a warning.
func (rt *RuntimeToon) PushChange(change dataChange) {
	select {
	case rt.changeChan <- change:
	default:
		zap.L().Warn("RuntimeToon: changeChan full, dropping change",
			zap.String("runtime_id", rt.ID.String()),
		)
	}
}

// SetFetchError stores the error that caused the runtime to fail so the fatal
// packet can surface a useful message instead of the generic fallback.
func (rt *RuntimeToon) SetFetchError(fe *toon.FetchError) {
	rt.fetchError = fe
}

// Unregister returns the unregister channel
func (rt *RuntimeToon) Unregister() chan uuid.UUID {
	return rt.unregister
}

// unregisterUUID returns the unregister channel as a send-only channel for use by wsruntime.BaseClient.
func (rt *RuntimeToon) unregisterUUID() chan<- uuid.UUID {
	return rt.unregister
}

// Register returns the register channel
func (rt *RuntimeToon) Register() chan Client {
	return rt.register
}

// HasClient checks if a client is connected
func (rt *RuntimeToon) HasClient(id uuid.UUID) bool {
	defer rt.clientMutex.RUnlock()
	rt.clientMutex.RLock()
	_, ok := rt.clients[id]
	return ok
}

// stop stops the runtime toon
func (rt *RuntimeToon) stop() {
	zap.L().Info("Toon runtime ended", zap.String("runtime_id", rt.ID.String()))

	rt.clientMutex.RLock()
	// unregister all clients
	for _, c := range rt.clients {
		c.close()
	}
	rt.clientMutex.RUnlock()

	rt.onEnd(rt.ID)
}

// run starts the runtime toon
func (rt *RuntimeToon) run() {
	rt.wg.Add(1)

	activityTimer := time.NewTimer(rt.opt.Timeout)
	clientExpire := time.NewTicker(rt.opt.ClientTimeoutCheckFreq)
	defer func() {

		// handle possible panics
		if r := recover(); r != nil {
			rt.logCritical("RuntimeToon.Panic", fmt.Errorf("panic recovered: %v", r))
			zap.L().Error("Panic in runtime, recovering and stopping",
				zap.Any("panic", r),
				zap.String("runtime_id", rt.ID.String()),
			)
		}

		activityTimer.Stop()
		clientExpire.Stop()
		rt.stop()
		rt.wg.Done()
	}()

	for {
		select {
		case cli := <-rt.register:
			activityTimer.Reset(rt.opt.Timeout)
			rt.handleClientConnect(cli)

		case id := <-rt.unregister:
			rt.handleClientDisconnect(id)

		case change := <-rt.changeChan:
			activityTimer.Reset(rt.opt.Timeout)
			rt.handleDataChange(change)

		case <-clientExpire.C:
			rt.runClientExpireChecker()

		case <-activityTimer.C:
			rt.stop()
			return

		case <-rt.done:
			rt.stop()
			return
		}
	}
}

// handleDataChange handles a data change
func (rt *RuntimeToon) handleDataChange(change dataChange) {
	switch change.Reason {
	case DataTypeCompleted:
		rt.handleDataChangeCompleted(change)
	case DataTypeFailed:
		rt.handleDataChangeFailed(change)
	case DataTypeProgress:
		rt.handleDataChangeProgress(change)
	}
}

// Client functions

// handleClientConnect handles a client connect
func (rt *RuntimeToon) handleClientConnect(client Client) {
	rt.registerClient(client)

	// Send initial packet with toon infos
	client.SendPacket(NewPacketInit())
}

// handleClientDisconnect handles a client disconnect
func (rt *RuntimeToon) handleClientDisconnect(id uuid.UUID) {
	defer func() {
		rt.unregisterClient(id)
	}()

	rt.clientMutex.RLock()
	_, ok := rt.clients[id]
	rt.clientMutex.RUnlock()
	if !ok {
		return
	}
}

// runClientExpireChecker handles client expiration
func (rt *RuntimeToon) runClientExpireChecker() {
	defer rt.clientMutex.Unlock()
	rt.clientMutex.Lock()

	now := time.Now()
	nb := 0

	for _, c := range rt.clients {
		if now.Sub(c.LastActivity()) > rt.opt.ClientTimeout {
			nb++
			c.close()
		}
	}

	if nb > 0 {
		zap.L().Info("Closed expired clients", zap.Int("nb", nb), zap.Duration("timeout", rt.opt.ClientTimeout))
	}
}

// Log functions

// logWarning logs a warning-level error to the async error logger
func (rt *RuntimeToon) logWarning(scope string, err error) {
	if rt.errorLogger == nil || err == nil {
		return
	}

	rt.errorLogger.LogError(supervisor.Error{
		Scope:    scope,
		Message:  err.Error(),
		Severity: "warning",
		Id:       rt.ID,
	})
}

// logError logs an error-level error to the async error logger
func (rt *RuntimeToon) logError(scope string, err error) {
	if rt.errorLogger == nil || err == nil {
		return
	}

	rt.errorLogger.LogError(supervisor.Error{
		Scope:    scope,
		Message:  err.Error(),
		Severity: "error",
		Id:       rt.ID,
	})
}

// logCritical logs a critical-level error to the async error logger
func (rt *RuntimeToon) logCritical(scope string, err error) {
	if rt.errorLogger == nil || err == nil {
		return
	}

	rt.errorLogger.LogError(supervisor.Error{
		Scope:    scope,
		Message:  err.Error(),
		Severity: "critical",
		Id:       rt.ID,
	})
}
