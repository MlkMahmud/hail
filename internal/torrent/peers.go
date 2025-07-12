package torrent


func (tr *Torrent) addPeer(p *peer) {
	tr.peersMu.Lock()
	defer tr.peersMu.Unlock()

	if _, ok := tr.peers[p.socket]; !ok {
		tr.peers[p.socket] = p
	}
}

func (tr *Torrent) getIdlePeer(filterFn func(p *peer) bool) (*peer, bool) {
	tr.peersMu.Lock()
	defer tr.peersMu.Unlock()

	if filterFn == nil {
		filterFn = func(p *peer) bool { return true }
	}

	for _, pr := range tr.peers {
		if !pr.isIdle() || !filterFn(pr) {
			continue
		}

		if pr.markAsBusy() {
			return pr, true
		}
	}

	return nil, false
}

func (tr *Torrent) numOfPeers() int {
	tr.peersMu.Lock()
	defer tr.peersMu.Unlock()

	return len(tr.peers)
}

func (tr *Torrent) removePeer(addr string) {
	tr.peersMu.Lock()
	defer tr.peersMu.Unlock()

	delete(tr.peers, addr)
}
