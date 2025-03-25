package client

import (
	"bytes"
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/codecrafters-io/bittorrent-starter-go/app/torrent"
	"github.com/codecrafters-io/bittorrent-starter-go/app/utils"
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
)

const (
	handshakeMessageLen = pstrLen + 49
	pstr                = "BitTorrent protocol"
	pstrLen             = len(pstr)
)

func generateBlockRequestPayload(block torrent.Block) []byte {
	blockBeginSize := 4
	blockIndexSize := 4
	blockLengthSize := 4
	messageIdSize := 1
	messageLenPrefixSize := 4
	messagePayloadSize := blockBeginSize + blockIndexSize + blockLengthSize
	messageBufferSize := messageLenPrefixSize + messageIdSize + messagePayloadSize

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

	return messageBuffer
}

func sendMessage(conn net.Conn, payload []byte, messageId MessageId) error {
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

	if _, err := utils.ConnWriteFull(conn, messageBuffer); err != nil {
		return err
	}

	return nil
}

func verifyHandshakeResponse(response []byte, expectedInfoHash [sha1.Size]byte) error {
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

func waitForMessage(conn net.Conn, messageId MessageId) (*Message, error) {
	messageLenBuffer := make([]byte, 4)

	if _, err := utils.ConnReadFull(conn, messageLenBuffer); err != nil {
		return nil, err
	}

	messageLen := binary.BigEndian.Uint32(messageLenBuffer)
	messageBuffer := make([]byte, messageLen)

	if _, err := utils.ConnReadFull(conn, messageBuffer); err != nil {
		return nil, err
	}

	receivedMessageId := MessageId(messageBuffer[0])

	if receivedMessageId != messageId {
		return nil, fmt.Errorf("expected received peer message Id to be %d, but got %d", messageId, receivedMessageId)
	}

	return &Message{Id: receivedMessageId, Payload: messageBuffer[1:]}, nil
}

func DownloadPiece(piece torrent.Piece, peer torrent.Peer, infoHash [sha1.Size]byte) ([]byte, error) {
	timeout := 3 * time.Second
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(peer.IpAddress, strconv.Itoa(int(peer.Port))), timeout)

	if err != nil {
		return nil, err
	}

	defer conn.Close()

	if _, err := EstablishHandshake(conn, infoHash); err != nil {
		return nil, err
	}

	if _, err := waitForMessage(conn, Bitfield); err != nil {
		return nil, err
	}

	if err := sendMessage(conn, nil, Interested); err != nil {
		return nil, err
	}

	if _, err := waitForMessage(conn, Unchoke); err != nil {
		return nil, err
	}

	blocks := piece.GetBlocks()
	numOfBlocks := len(blocks)
	numOfBlocksDownloaded := 0

	downloadedBlocks := make([]torrent.Block, numOfBlocks)

	for numOfBlocksDownloaded < numOfBlocks {
		payload := generateBlockRequestPayload(blocks[numOfBlocksDownloaded])

		if err := sendMessage(conn, payload, Interested); err != nil {
			return nil, err
		}

		message, err := waitForMessage(conn, PieceMessageId)

		if err != nil {
			return nil, err
		}

		index := 0

		blockPieceIndex := binary.BigEndian.Uint32(message.Payload[index:])
		index += 4

		blockPieceOffset := binary.BigEndian.Uint32(message.Payload[index:])
		index += 4

		blockData := message.Payload[index:]

		downloadedBlockIndex := int(blockPieceOffset / torrent.BlockSize)

		if downloadedBlockIndex >= numOfBlocks {
			return nil, fmt.Errorf("downloaded block offset %d is invalid", blockPieceOffset)
		}

		downloadedBlocks[downloadedBlockIndex] = torrent.Block{
			Begin:     int(blockPieceOffset),
			Data:      blockData,
			Length:    len(blockData),
			PiceIndex: int(blockPieceIndex),
		}

		numOfBlocksDownloaded += 1
	}

	return piece.AssembleBlocks(downloadedBlocks), nil
}

func EstablishHandshake(conn net.Conn, infoHash [sha1.Size]byte) ([]byte, error) {
	peerId := []byte(utils.GenerateRandomString(20, ""))
	messageBuffer := make([]byte, handshakeMessageLen)
	messageBuffer[0] = byte(pstrLen)

	index := 1
	index += copy(messageBuffer[index:], []byte(pstr))
	index += copy(messageBuffer[index:], make([]byte, 8))
	index += copy(messageBuffer[index:], infoHash[:])
	index += copy(messageBuffer[index:], peerId[:])

	if _, err := utils.ConnWriteFull(conn, messageBuffer); err != nil {
		return nil, err
	}

	responseBuffer := make([]byte, handshakeMessageLen)

	if _, err := utils.ConnReadFull(conn, responseBuffer); err != nil {
		return nil, err
	}

	if err := verifyHandshakeResponse(responseBuffer, infoHash); err != nil {
		return nil, err
	}

	return responseBuffer, nil
}
