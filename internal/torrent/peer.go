package torrent

import (
	"fmt"
)

type peer struct {
	ipAddress string
	port      uint16
}

func (p peer) String() string {
	return fmt.Sprintf("%s:%d", p.ipAddress, p.port)
}
