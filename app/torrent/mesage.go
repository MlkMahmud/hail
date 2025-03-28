package torrent

type Extension string

const (
	Metadata Extension = "ut_metadata"
)

type Message struct {
	Id      MessageId
	Payload []byte
}

type MessageId int

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
	ExtensionMessageId = 20
)
