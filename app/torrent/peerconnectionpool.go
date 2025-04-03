package torrent

import (
	"runtime"
	"sync"
)

type PeerConnectionPool struct {
	Connections map[string]PeerConnection
	mutex       sync.Mutex
}

const (
	maxNumOfPeerConnections = 30
)

func NewPeerConnectionPool() *PeerConnectionPool {
	return new(PeerConnectionPool)
}

func (p *PeerConnectionPool) AddPeerConnectionToPool(peerConnection PeerConnection) {
	p.mutex.Lock()
	p.Connections[peerConnection.PeerAddress] = peerConnection
	p.mutex.Unlock()
}

func (p *PeerConnectionPool) DrainConnectionPool() {
	for _, peerConnection := range p.Connections {
		if peerConnection.Conn != nil {
			peerConnection.Conn.Close()
		}
	}

	p.Connections = make(map[string]PeerConnection)
}

func (p *PeerConnectionPool) InitPeerConnectionPool(peers []Peer) {
	peerConnectionPoolSize := min(len(peers), 2*runtime.NumCPU(), maxNumOfPeerConnections)

	for i := range peerConnectionPoolSize {
		peerConnection := NewPeerConnection(PeerConnectionConfig{Peer: peers[i]})
		p.Connections[peerConnection.PeerAddress] = *peerConnection
	}
}

func (p *PeerConnectionPool) RemovePeerConnectionFromPool(peerAddress string, onConnectionPoolDrained func()) {
	p.mutex.Lock()
	delete(p.Connections, peerAddress)

	if len(p.Connections) == 0 && onConnectionPoolDrained != nil {
		onConnectionPoolDrained()
	}

	p.mutex.Unlock()
}

func (p *PeerConnectionPool) Size() int {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	num := len(p.Connections)
	return num
}
