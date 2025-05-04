package torrent

import (
	"crypto/sha1"
	"fmt"
)

type Peer struct {
	InfoHash  [sha1.Size]byte
	IpAddress string
	Port      uint16
}

func (p Peer) String() string {
	return fmt.Sprintf("%s:%d", p.IpAddress, p.Port)
}
