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

func (m messageId) String() string {
	switch m {
	case choke:
		return "choke"
	case unchokeMessageId:
		return "unchoke"
	case interestedMessageId:
		return "interested"
	case notInterestedMessageId:
		return "not interested"
	case haveMessageId:
		return "have"
	case bitfieldMessageId:
		return "bitfield"
	case requestMessageId:
		return "request"
	case pieceMessageId:
		return "piece"
	case cancelMessageId:
		return "cancel"
	case extensionMessageId:
		return "extension"
	default:
		return ""
	}
}
