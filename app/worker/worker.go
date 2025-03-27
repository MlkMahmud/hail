package worker

import (
	"bytes"
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/codecrafters-io/bittorrent-starter-go/app/bencode"
	"github.com/codecrafters-io/bittorrent-starter-go/app/torrent"
	"github.com/codecrafters-io/bittorrent-starter-go/app/utils"
)

type Message struct {
	Id      MessageId
	Payload []byte
}

type MessageId int

type Worker struct {
	Conn     net.Conn
	InfoHash [sha1.Size]byte
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

const (
	handshakeMessageLen = pstrLen + 49
	pstr                = "BitTorrent protocol"
	pstrLen             = len(pstr)
)

func NewWorker(peer torrent.Peer, infoHash [sha1.Size]byte) (*Worker, error) {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(peer.IpAddress, strconv.Itoa(int(peer.Port))), 5*time.Second)

	if err != nil {
		return nil, err
	}

	return &Worker{
		Conn:     conn,
		InfoHash: infoHash,
	}, nil
}

func (w *Worker) SupportsExtensions(handshakeResponse []byte) bool {
	if len(handshakeResponse) != handshakeMessageLen {
		return false
	}

	if reservedBitsIndex, reservedBitsLength := 21, 8; bytes.Equal(make([]byte, 8), handshakeResponse[reservedBitsIndex:reservedBitsIndex+reservedBitsLength]) {
		return false
	}

	return true

}

func (w *Worker) EstablishHandshake() ([]byte, error) {
	peerId := []byte(utils.GenerateRandomString(20, ""))
	messageBuffer := make([]byte, handshakeMessageLen)
	messageBuffer[0] = byte(pstrLen)

	index := 1
	index += copy(messageBuffer[index:], []byte(pstr))
	index += copy(messageBuffer[index:], make([]byte, 5))

	messageBuffer[index] = byte(16)
	index += 1

	index += copy(messageBuffer[index:], make([]byte, 2))
	index += copy(messageBuffer[index:], w.InfoHash[:])
	index += copy(messageBuffer[index:], peerId[:])

	if _, err := utils.ConnWriteFull(w.Conn, messageBuffer); err != nil {
		return nil, err
	}

	responseBuffer := make([]byte, handshakeMessageLen)

	if _, err := utils.ConnReadFull(w.Conn, responseBuffer); err != nil {
		return nil, err
	}

	responseLength := len(responseBuffer)

	if responseLength != handshakeMessageLen {
		return nil, fmt.Errorf("expected response message length to be '%d' long, but got '%d'", handshakeMessageLen, responseLength)
	}

	if receivedPstrLen := responseBuffer[0]; receivedPstrLen != byte(pstrLen) {
		return nil, fmt.Errorf("expected protocol string length to be '%d', but got '%v'", pstrLen, receivedPstrLen)
	}

	if receivedPstr := responseBuffer[1 : pstrLen+1]; string(receivedPstr) != pstr {
		return nil, fmt.Errorf("expected protocol string to equal '%s', but got '%s'", pstr, receivedPstr)
	}

	if receivedInfoHash := responseBuffer[28:48]; !bytes.Equal(receivedInfoHash, w.InfoHash[:]) {
		return nil, fmt.Errorf("received info hash %v does not match expected info hash %v", receivedInfoHash, w.InfoHash)
	}

	return responseBuffer, nil
}

func (w *Worker) ReceiveMessage(messageId MessageId) (*Message, error) {
	messageLengthBuffer := make([]byte, 4)

	if _, err := utils.ConnReadFull(w.Conn, messageLengthBuffer); err != nil {
		return nil, err
	}

	messageLength := binary.BigEndian.Uint32(messageLengthBuffer)
	messageBuffer := make([]byte, messageLength)

	if _, err := utils.ConnReadFull(w.Conn, messageBuffer); err != nil {
		return nil, err
	}

	receivedMessageId := MessageId(messageBuffer[0])

	if receivedMessageId != messageId {
		return nil, fmt.Errorf("expected received message Id to be %d, but got %d", messageId, receivedMessageId)
	}

	return &Message{Id: receivedMessageId, Payload: messageBuffer[1:]}, nil
}

func (w *Worker) SendExtensionHandshake() error {
	bencodedString, err := bencode.EncodeValue(map[string]any{
		"m": map[string]any{
			"ut_metadata": 1,
		},
	})

	if err != nil {
		return fmt.Errorf("failed to generate extension handshake payload: %w", err)
	}

	messageIdLength := 1
	messagePayloadLength := len(bencodedString) + 1 // one extra byte for the extension message Id (different from the message Id)
	messagePrefixLength := 4

	messageBuffer := make([]byte, messageIdLength+messagePayloadLength+messagePrefixLength)
	binary.BigEndian.PutUint32(messageBuffer, uint32(messageIdLength+messagePayloadLength))

	index := 4
	// message Id for an extension Id is 20
	messageBuffer[index] = byte(Extension)
	index += 1

	messageBuffer[index] = byte(0) // write the extension message Id (0)
	index += 1

	copy(messageBuffer[index:], []byte(bencodedString))

	if _, err := utils.ConnWriteFull(w.Conn, messageBuffer); err != nil {
		return fmt.Errorf("failed to send extension handshake message: %w", err)
	}

	return nil
}

func (w *Worker) SendMessage(messageId MessageId, payload []byte) error {
	messageIdLen := 1
	messagePrefixLen := 4
	payloadLen := 0

	if payload != nil {
		payloadLen = len(payload)
	}

	messageBufferLen := messagePrefixLen + messageIdLen + payloadLen
	messageBuffer := make([]byte, messageBufferLen)
	binary.BigEndian.PutUint32(messageBuffer, uint32(messageIdLen+payloadLen))

	index := 4
	messageBuffer[index] = byte(messageId)
	copy(messageBuffer[index+1:], payload)

	if _, err := utils.ConnWriteFull(w.Conn, messageBuffer); err != nil {
		return err
	}

	return nil
}
