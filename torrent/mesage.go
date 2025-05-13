package torrent

type extension string

type extensionMessage int
type message struct {
	id      messageId
	payload []byte
}

type messageId int

const (
	metadataExt extension = "ut_metadata"
)

const (
	extensionRequestMessageId extensionMessage = iota
	extensionDataMessageId
	extensionRejectMessageId
)

const (
	choke messageId = iota
	unchokeMessageId
	interestedMessageId
	notInterestedMessageId
	haveMessageId
	bitfieldMessageId
	requestMessageId
	pieceMessageId
	cancelMessageId
	extensionMessageId = 20
)
