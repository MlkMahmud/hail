package torrent

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/MlkMahmud/hail/utils"
)

// todo: gracefully drain connections

type peerConnectionPool struct {
	activeConnectionIds utils.Set
	connections         map[string]PeerConnection
	mutex               sync.Mutex
}

func newPeerConnectionPool() *peerConnectionPool {
	return &peerConnectionPool{
		activeConnectionIds: *utils.NewSet(),
		connections:         make(map[string]PeerConnection),
	}
}

func (p *peerConnectionPool) addConnection(peerConnection PeerConnection) {
	p.mutex.Lock()
	p.connections[peerConnection.peerAddress] = peerConnection
	p.mutex.Unlock()
}

func (p *peerConnectionPool) closeConnections() {
	for _, peerConnection := range p.connections {
		peerConnection.Close()
	}

	p.connections = make(map[string]PeerConnection)
}

func (p *peerConnectionPool) getIdleConnection(ctx context.Context) (PeerConnection, error) {
	for {
		p.mutex.Lock()

		for _, peerConnection := range p.connections {
			if !p.activeConnectionIds.Contains(peerConnection.peerAddress) {
				p.activeConnectionIds.Add(peerConnection.peerAddress)
				p.mutex.Unlock()
				return peerConnection, nil
			}
		}

		p.mutex.Unlock()

		select {
		case <-ctx.Done():
			return PeerConnection{}, fmt.Errorf("context canceled: %w", ctx.Err())
		default:
			// todo: addd trigger to find more peers
			time.Sleep(2 * time.Second)
		}
	}
}

func (p *peerConnectionPool) releaseActiveConnection(pc PeerConnection) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	if !p.activeConnectionIds.Contains(pc.peerAddress) {
		return
	}

	p.activeConnectionIds.Remove(pc.peerAddress)
}

func (p *peerConnectionPool) removeConnection(peerAddress string) {
	p.mutex.Lock()
	delete(p.connections, peerAddress)
	p.mutex.Unlock()
}

func (p *peerConnectionPool) size() int {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	num := len(p.connections)
	return num
}
