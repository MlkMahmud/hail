package torrent

import (
	"errors"
)

var (
	errNoIdlePeers = errors.New("there are currently no idle peers")
)

func (tr *Torrent) addPeer(p *peer) {
	tr.peersMu.Lock()
	defer tr.peersMu.Unlock()

	addr := p.String()

	if _, ok := tr.peers[addr]; !ok {
		tr.peers[addr] = p
	}
}

func (tr *Torrent) getIdlePeer() (*peer, error) {
	tr.peersMu.Lock()
	defer tr.peersMu.Unlock()

	for addr, pr := range tr.peers {
		if !tr.activePeerIds.Has(addr) {
			tr.activePeerIds.Add(addr)
			return pr, nil
		}
	}

	return nil, errNoIdlePeers
}

func (tr *Torrent) numOfPeers() int {
	tr.peersMu.Lock()
	defer tr.peersMu.Unlock()

	return len(tr.peers)
}

func (tr *Torrent) removePeer(addr string) {
	tr.peersMu.Lock()
	defer tr.peersMu.Unlock()

	if tr.activePeerIds.Has(addr) {
		tr.activePeerIds.Remove(addr)
	}

	delete(tr.peers, addr)
}
