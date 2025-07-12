package torrent

import (
	"sync"
)

type peer struct {
	ipAddress string
	isBusy    bool
	port      uint16
	socket    string

	mu sync.Mutex
}

func (p *peer) isIdle() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return !p.isBusy
}

func (p *peer) markAsBusy() bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.isBusy {
		return false
	}

	p.isBusy = true
	return true
}

func (p *peer) markAsIdle() bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.isBusy {
		return false
	}

	p.isBusy = false
	return true
}
