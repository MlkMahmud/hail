package torrent

import (
	"bytes"
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"math"
	"net"
	"strconv"
	"time"

	"github.com/codecrafters-io/bittorrent-starter-go/app/bencode"
	"github.com/codecrafters-io/bittorrent-starter-go/app/utils"
)

type PeerConnection struct {
	Conn               net.Conn
	Extensions         map[Extension]uint8
	InfoHash           [sha1.Size]byte
	Peer               Peer
	SupportsExtensions bool
}

const (
	handshakeMessageLen = pstrLen + 49
	pstr                = "BitTorrent protocol"
	pstrLen             = len(pstr)
)

func NewPeerConnection(peer Peer, infoHash [sha1.Size]byte) (*PeerConnection, error) {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(peer.IpAddress, strconv.Itoa(int(peer.Port))), 5*time.Second)

	if err != nil {
		return nil, err
	}

	return &PeerConnection{
		Conn:     conn,
		InfoHash: infoHash,
		Peer:     peer,
	}, nil
}

func (p *PeerConnection) EstablishHandshake() ([]byte, error) {
	peerId := []byte(utils.GenerateRandomString(20, ""))
	messageBuffer := make([]byte, handshakeMessageLen)
	messageBuffer[0] = byte(pstrLen)

	index := 1
	index += copy(messageBuffer[index:], []byte(pstr))
	index += copy(messageBuffer[index:], make([]byte, 5))

	messageBuffer[index] = byte(16)
	index += 1

	index += copy(messageBuffer[index:], make([]byte, 2))
	index += copy(messageBuffer[index:], p.InfoHash[:])
	index += copy(messageBuffer[index:], peerId[:])

	if _, err := utils.ConnWriteFull(p.Conn, messageBuffer); err != nil {
		return nil, err
	}

	responseBuffer := make([]byte, handshakeMessageLen)

	if _, err := utils.ConnReadFull(p.Conn, responseBuffer); err != nil {
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

	if receivedInfoHash := responseBuffer[28:48]; !bytes.Equal(receivedInfoHash, p.InfoHash[:]) {
		return nil, fmt.Errorf("received info hash %v does not match expected info hash %v", receivedInfoHash, p.InfoHash)
	}

	if reservedBitsIndex, reservedBitsLength := 21, 8; !bytes.Equal(make([]byte, 8), responseBuffer[reservedBitsIndex:reservedBitsIndex+reservedBitsLength]) {
		p.SupportsExtensions = true
	}

	return responseBuffer, nil
}

func (p *PeerConnection) ReceiveExtensionHandshakeMessage() error {
	message, err := p.ReceiveMessage(ExtensionMessageId)

	if err != nil {
		return fmt.Errorf("failed to receive extension handshake message %w", err)
	}

	// Ignore the first byte of the payload which contains the extension message ID.
	decodedPayload, _, err := bencode.DecodeValue(message.Payload[1:])

	if err != nil {
		return fmt.Errorf("failed to decode extension handshake message payload %w", err)
	}

	dict, ok := decodedPayload.(map[string]any)

	if !ok {
		return fmt.Errorf("expected decoded payload to be a dictionary, but received %v", dict)
	}

	extensionsMap, ok := dict["m"].(map[string]any)
	extensions := make(map[Extension]uint8)

	if !ok {
		return fmt.Errorf("expected decoded payload to include an \"m\" key which maps to a dictionary of supported extensions, but got %v", extensionsMap)
	}

	for _, ext := range []Extension{Metadata} {
		value, ok := extensionsMap[string(ext)]

		if !ok {
			continue
		}

		id, ok := value.(int)

		if !ok {
			return fmt.Errorf("expected extension Id to be an integer, but received %v", id)
		}

		if id < 0 || id > math.MaxUint8 {
			return fmt.Errorf("expected extension Id to be an integer between '0 - 255', but received %v", id)
		}

		extensions[ext] = uint8(id)
	}

	p.Extensions = extensions

	return nil
}

func (p *PeerConnection) ReceiveMessage(messageId MessageId) (*Message, error) {
	messageLengthBuffer := make([]byte, 4)

	if _, err := utils.ConnReadFull(p.Conn, messageLengthBuffer); err != nil {
		return nil, err
	}

	messageLength := binary.BigEndian.Uint32(messageLengthBuffer)
	messageBuffer := make([]byte, messageLength)

	if _, err := utils.ConnReadFull(p.Conn, messageBuffer); err != nil {
		return nil, err
	}

	receivedMessageId := MessageId(messageBuffer[0])

	if receivedMessageId != messageId {
		return nil, fmt.Errorf("expected received message Id to be %d, but got %d", messageId, receivedMessageId)
	}

	return &Message{Id: receivedMessageId, Payload: messageBuffer[1:]}, nil
}

func (p *PeerConnection) SendExtensionHandshakeMessage() error {
	bencodedString, err := bencode.EncodeValue(map[string]any{
		"m": map[string]any{
			"ut_metadata": 1,
		},
	})

	if err != nil {
		return fmt.Errorf("failed to generate extension handshake payload: %w", err)
	}

	messagePayloadLength := len(bencodedString) + 1 // one extra byte for the extension message Id (different from the message Id)
	messagePayloadBuffer := make([]byte, messagePayloadLength)

	index := 0

	messagePayloadBuffer[index] = byte(0) // write the extension message Id (0)
	index += 1

	copy(messagePayloadBuffer[index:], []byte(bencodedString))

	if err := p.SendMessage(ExtensionMessageId, messagePayloadBuffer); err != nil {
		return fmt.Errorf("failed to send extension handshake message: %w", err)
	}

	return nil
}

func (p *PeerConnection) SendMessage(messageId MessageId, payload []byte) error {
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

	if _, err := utils.ConnWriteFull(p.Conn, messageBuffer); err != nil {
		return err
	}

	return nil
}
