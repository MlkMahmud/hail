package torrent

import (
	"bufio"
	"bytes"
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"

	"github.com/codecrafters-io/bittorrent-starter-go/app/utils"
)

type MessageId int

type Message struct {
	Id      MessageId
	Payload []byte
}

type Peer struct {
	IpAddress string
	Port      uint16
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

	Generic
)

const (
	handshakeMessageLen = pstrLen + 49
	pstr                = "BitTorrent protocol"
	pstrLen             = len(pstr)
)

func isValidMessageId(Id byte) bool {
	return Id <= 8
}

func (p *Peer) assembleBlocks(blocks []Block, pieceLength int) []byte {
	buffer := make([]byte, pieceLength)

	for _, block := range blocks {
		copy(buffer[block.Begin:], block.Data)
	}

	return buffer
}

func (p *Peer) requestBlock(writer bufio.Writer, block Block) error {
	blockBeginSize := 4
	blockIndexSize := 4
	blockLengthSize := 4
	messageIdSize := 1
	mesageLenPrefixSize := 4
	messagePayloadSize := blockBeginSize + blockIndexSize + blockLengthSize
	messageBufferSize := mesageLenPrefixSize + messageIdSize + messagePayloadSize

	messageBuffer := make([]byte, messageBufferSize)
	binary.BigEndian.PutUint32(messageBuffer, uint32(messageIdSize+messagePayloadSize))

	index := 4

	messageBuffer[index] = byte(Request)
	index += messageIdSize

	binary.BigEndian.PutUint32(messageBuffer[index:], uint32(block.PiceIndex))
	index += blockIndexSize

	binary.BigEndian.PutUint32(messageBuffer[index:], uint32(block.Begin))
	index += blockBeginSize

	binary.BigEndian.PutUint32(messageBuffer[index:], uint32(block.Length))
	index += blockLengthSize

	if _, err := writer.Write(messageBuffer); err != nil {
		return err
	}

	if err := writer.Flush(); err != nil {
		return err
	}

	return nil

}

func (p *Peer) sendMessage(writer bufio.Writer, payload []byte, messageId MessageId) error {
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

	if _, err := writer.Write(messageBuffer); err != nil {
		return err
	}

	if err := writer.Flush(); err != nil {
		return err
	}

	return nil
}

func (p *Peer) verifyHandshakeResponse(response []byte, expectedInfoHash [sha1.Size]byte) error {
	responseLen := len(response)

	if responseLen != handshakeMessageLen {
		return fmt.Errorf("expected response message length to be '%d' long, but got '%d'", handshakeMessageLen, responseLen)
	}

	if receivedPstrLen := response[0]; receivedPstrLen != byte(pstrLen) {
		return fmt.Errorf("expected protocol string length to be '%d', but got '%v'", pstrLen, receivedPstrLen)
	}

	if receivedPstr := response[1 : pstrLen+1]; string(receivedPstr) != pstr {
		return fmt.Errorf("expected protocol string to equal '%s', but got '%s'", pstr, receivedPstr)
	}

	if receivedInfoHash := response[28:48]; !bytes.Equal(receivedInfoHash, expectedInfoHash[:]) {
		return fmt.Errorf("received info hash %v does not match expected info hash %v", receivedInfoHash, expectedInfoHash)
	}

	return nil
}

func (p *Peer) waitForMessage(reader bufio.Reader, messageId MessageId) (*Message, error) {
	messageLenBuf := make([]byte, 4)

	if _, err := io.ReadFull(&reader, messageLenBuf); err != nil {
		return nil, err
	}

	messageLen := binary.BigEndian.Uint32(messageLenBuf)
	messageBuffer := make([]byte, messageLen)

	if _, err := io.ReadFull(&reader, messageBuffer); err != nil {
		return nil, err
	}

	receivedMessageId := messageBuffer[0]

	if !isValidMessageId(receivedMessageId) {
		return nil, fmt.Errorf("received peer message Id '%v' is invalid", receivedMessageId)
	}

	if messageId != Generic && receivedMessageId != byte(messageId) {
		return nil, fmt.Errorf("expected received peer message Id to be %v, but got %v", byte(messageId), receivedMessageId)
	}

	return &Message{Id: MessageId(receivedMessageId), Payload: messageBuffer[1:]}, nil
}

func (p *Peer) DownloadPiece(piece Piece, infoHash [sha1.Size]byte) ([]byte, error) {
	timeout := 3 * time.Second
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(p.IpAddress, strconv.Itoa(int(p.Port))), timeout)

	if err != nil {
		return nil, err
	}

	defer conn.Close()

	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)

	if err := conn.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return nil, err
	}

	if _, err := p.EstablishHandshake(conn, infoHash); err != nil {
		return nil, err
	}

	if _, err := p.waitForMessage(*reader, Bitfield); err != nil {
		return nil, err
	}

	if err := p.sendMessage(*writer, nil, Interested); err != nil {
		return nil, err
	}

	if _, err := p.waitForMessage(*reader, Unchoke); err != nil {
		return nil, err
	}

	blocks := piece.GetPieceBlocks()

	for _, block := range blocks {
		if err := p.requestBlock(*writer, block); err != nil {
			return nil, err
		}
	}

	numOfBlocks := len(blocks)
	numOfBlocksDownloaded := 0

	downloadedBlocks := make([]Block, numOfBlocks)

	for numOfBlocksDownloaded < numOfBlocks {
		message, err := p.waitForMessage(*reader, Generic)

		if err != nil {
			return nil, err
		}

		if message.Id != PieceMessageId {
			return nil, fmt.Errorf("received an unexpected message type '%d'", message.Id)
		}

		index := 0

		blockPieceIndex := binary.BigEndian.Uint32(message.Payload[index:])
		index += 4

		blockPieceOffset := binary.BigEndian.Uint32(message.Payload[index:])
		index += 4

		blockData := message.Payload[index:]

		downloadedBlockIndex := int(blockPieceOffset / BlockSize)

		if downloadedBlockIndex >= numOfBlocks {
			return nil, fmt.Errorf("downloaded block offset %d is invalid", blockPieceOffset)
		}

		downloadedBlocks[downloadedBlockIndex] = Block{
			Begin:     int(blockPieceOffset),
			Data:      blockData,
			Length:    len(blockData),
			PiceIndex: int(blockPieceIndex),
		}

		numOfBlocksDownloaded += 1
	}

	return p.assembleBlocks(downloadedBlocks, piece.Length), nil
}

func (p *Peer) EstablishHandshake(conn net.Conn, infoHash [sha1.Size]byte) ([]byte, error) {
	peerId := []byte(utils.GenerateRandomString(20, ""))
	messageBuf := make([]byte, handshakeMessageLen)
	messageBuf[0] = byte(pstrLen)

	index := 1
	index += copy(messageBuf[index:], []byte(pstr))
	index += copy(messageBuf[index:], make([]byte, 8))
	index += copy(messageBuf[index:], infoHash[:])
	index += copy(messageBuf[index:], peerId[:])

	_, writeErr := conn.Write(messageBuf)

	if writeErr != nil {
		return nil, writeErr
	}

	respBuf := make([]byte, handshakeMessageLen)
	read, err := conn.Read(respBuf)

	if err != nil {
		return nil, err
	}

	if read != handshakeMessageLen {
		return nil, fmt.Errorf("unexpected end of input")
	}

	if err := p.verifyHandshakeResponse(respBuf, infoHash); err != nil {
		return nil, err
	}

	return respBuf, nil
}
