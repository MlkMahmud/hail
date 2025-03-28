package torrent

import (
	"crypto/sha1"
	"net"
)

type Message struct {
	Id      MessageId
	Payload []byte
}

type MessageId int

type PeerConnection struct {
	Conn     net.Conn
	InfoHash [sha1.Size]byte
	Peer     Peer
}

const (
	Choke MessageId = iota
	Unchoke
	Interested
	NotInterested
	Have
	Bitfield
	Request
	PieceMessageId
	Cancel
	Extension = 20
)
