package torrent

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

func (tr *Torrent) addPeer(p *peer) {
	tr.peersMu.Lock()
	defer tr.peersMu.Unlock()

	if _, ok := tr.peers[p.socket]; !ok {
		tr.peers[p.socket] = p
	}
}

func (tr *Torrent) getIdlePeer(filterFn func(p *peer) bool) *peer {
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
			return pr
		}
	}

	return nil
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

// Manages a request for additional peers in the torrent.
//
// It registers a pending peer request identified by a unique request ID,
// and waits for either the request to be fulfilled (signaled via reqCh)
// or for the provided context to be cancelled. Upon completion, it ensures
// the pending request is cleaned up from the tracking map.
func (tr *Torrent) requestMorePeers(ctx context.Context) {
	tr.logger.Debug("requesting for more peers...")

	reqId := uuid.New().String()
	reqCh := make(chan struct{})

	tr.pendingPeerRequestsMu.Lock()
	tr.pendingPeerRequests[reqId] = reqCh
	tr.pendingPeerRequestsMu.Unlock()

	// attempt to manually trigger announcer routine.
	select {
	case tr.triggerAnnounceCh <- struct{}{}:

	default:
	}

	defer func() {
		tr.pendingPeerRequestsMu.Lock()
		delete(tr.pendingPeerRequests, reqId)
		tr.pendingPeerRequestsMu.Unlock()
	}()

	select {
	case <-ctx.Done():
		return

	case <-reqCh:
		return
	}
}

// Closes all channels in the pendingPeerRequests map to signal
// that all pending peer requests should be cancelled or completed. After closing the channels,
// it resets the pendingPeerRequests map to an empty state. This method is safe for concurrent use.
func (tr *Torrent) signalAllPendingRequests() {
	tr.pendingPeerRequestsMu.Lock()
	defer tr.pendingPeerRequestsMu.Unlock()

	if len(tr.pendingPeerRequests) < 1 {
		return
	}

	tr.logger.Debug("sending completion signal to all pending peer requesters...")

	for _, ch := range tr.pendingPeerRequests {
		close(ch)
	}

	tr.logger.Debug(fmt.Sprintf("successfully sent completion signal to %d requesters", len(tr.pendingPeerRequests)))
	tr.pendingPeerRequests = make(map[string]chan struct{})
}
