package torrent

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/MlkMahmud/hail/internal/utils"
)

// todo: gracefully drain connections

type peerConnectionPool struct {
	activeConnectionIds utils.Set
	connections         map[string]*peerConnection
	mutex               sync.Mutex
}

func newPeerConnectionPool() *peerConnectionPool {
	return &peerConnectionPool{
		activeConnectionIds: *utils.NewSet(),
		connections:         make(map[string]*peerConnection),
	}
}

func (p *peerConnectionPool) addConnection(pc *peerConnection) {
	p.mutex.Lock()
	p.connections[pc.remotePeerAddress] = pc
	p.mutex.Unlock()
}

func (p *peerConnectionPool) closeConnections() {
	for _, pc := range p.connections {
		pc.close()
	}
}

func (p *peerConnectionPool) getIdleConnection(ctx context.Context) (*peerConnection, error) {
	for {
		p.mutex.Lock()

		for id, pc := range p.connections {
			if !p.activeConnectionIds.Contains(id) {
				p.activeConnectionIds.Add(id)
				p.mutex.Unlock()
				return pc, nil
			}
		}

		p.mutex.Unlock()

		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("failed to find idle peer connection: %w", ctx.Err())
		default:
			// todo: add trigger to find more peers
			time.Sleep(2 * time.Second)
		}
	}
}

func (p *peerConnectionPool) removeConnection(peerAddress string) {
	p.mutex.Lock()

	if p.activeConnectionIds.Contains(peerAddress) {
		p.activeConnectionIds.Remove(peerAddress)
	}

	delete(p.connections, peerAddress)
	p.mutex.Unlock()
}

func (p *peerConnectionPool) size() int {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	num := len(p.connections)
	return num
}
