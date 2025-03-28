package torrent

type Extension string

type ExtensionMessage int
type Message struct {
	Id      MessageId
	Payload []byte
}

type MessageId int

const (
	Metadata Extension = "ut_metadata"
)

const (
	ExtensionRequestMessageId ExtensionMessage = iota
	ExtensionDataMessageId
	ExtensionRejectMessageId
)

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
