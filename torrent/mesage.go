package torrent

type message struct {
	id      messageId
	payload []byte
}

type messageId int

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
	keepAliveMessageId = -1
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
	case keepAliveMessageId:
		return "keep alive"
	default:
		return ""
	}
}

type messageRequest struct {
	errorCh    chan error
	responseCh chan []byte
}
