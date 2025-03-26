package client

import (
	"bytes"
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/codecrafters-io/bittorrent-starter-go/app/torrent"
	"github.com/codecrafters-io/bittorrent-starter-go/app/utils"
)

type DownloadedPiece struct {
	Data  []byte
	Err   error
	Piece torrent.Piece
}

type Message struct {
	Id      MessageId
	Payload []byte
}

type MessageId int
type ReadWriteMutex struct {
	reader sync.Mutex
	writer sync.Mutex
}

type RequestMessageResult struct {
	block torrent.Block
	err   error
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
)

const (
	handshakeMessageLen = pstrLen + 49
	pstr                = "BitTorrent protocol"
	pstrLen             = len(pstr)
)

func downloadBlock(conn net.Conn, block torrent.Block, resultQueue chan<- RequestMessageResult, mutex *ReadWriteMutex) {
	payload := generateBlockRequestPayload(block)

	mutex.writer.Lock()
	err := sendMessage(conn, Request, payload)
	mutex.writer.Unlock()

	if err != nil {
		resultQueue <- RequestMessageResult{err: err}
		return
	}

	mutex.reader.Lock()
	message, err := waitForMessage(conn, PieceMessageId)
	mutex.reader.Unlock()

	if err != nil {
		resultQueue <- RequestMessageResult{err: err}
		return
	}

	index := 0

	blockPieceIndex := binary.BigEndian.Uint32(message.Payload[index:])
	index += 4

	blockPieceOffset := binary.BigEndian.Uint32(message.Payload[index:])
	index += 4

	blockData := message.Payload[index:]

	resultQueue <- RequestMessageResult{
		block: torrent.Block{
			Begin:     int(blockPieceOffset),
			Data:      blockData,
			Length:    len(blockData),
			PiceIndex: int(blockPieceIndex),
		},
	}

	return
}

func generateBlockRequestPayload(block torrent.Block) []byte {
	blockBeginSize := 4
	blockIndexSize := 4
	blockLengthSize := 4
	messageBufferSize := blockBeginSize + blockIndexSize + blockLengthSize

	messageBuffer := make([]byte, messageBufferSize)

	index := 0

	binary.BigEndian.PutUint32(messageBuffer[index:], uint32(block.PiceIndex))
	index += blockIndexSize

	binary.BigEndian.PutUint32(messageBuffer[index:], uint32(block.Begin))
	index += blockBeginSize

	binary.BigEndian.PutUint32(messageBuffer[index:], uint32(block.Length))
	index += blockLengthSize

	return messageBuffer
}

func sendMessage(conn net.Conn, messageId MessageId, payload []byte) error {
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

func DownloadPiece(piece torrent.Piece, peer torrent.Peer, infoHash [sha1.Size]byte) (*DownloadedPiece, error) {
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

	if err := sendMessage(conn, Interested, nil); err != nil {
		return nil, err
	}

	if _, err := waitForMessage(conn, Unchoke); err != nil {
		return nil, err
	}

	blocks := piece.GetBlocks()
	numOfBlocks := len(blocks)
	numOfBlocksDownloaded := 0

	downloadedBlocks := make([]torrent.Block, numOfBlocks)
	mutex := ReadWriteMutex{}
	requestBatchSize := 3
	resultsQueue := make(chan RequestMessageResult, requestBatchSize)

	for numOfBlocksDownloaded < numOfBlocks {
		pendingBlocks := blocks[numOfBlocksDownloaded:]
		numOfPendingBlocks := len(pendingBlocks)

		currentBatchSize := min(numOfPendingBlocks, requestBatchSize)

		for i := 0; i < currentBatchSize; i++ {
			go downloadBlock(conn, pendingBlocks[i], resultsQueue, &mutex)
		}

		for i := 0; i < currentBatchSize; i++ {
			requestResult := <-resultsQueue

			if requestResult.err != nil {
				return nil, err
			}

			downloadedBlock := requestResult.block
			downloadedBlockIndex := int(downloadedBlock.Begin / torrent.BlockSize)

			if downloadedBlockIndex >= numOfBlocks {
				return nil, fmt.Errorf("downloaded block offset %d is invalid", downloadedBlock.Begin)
			}

			downloadedBlocks[downloadedBlockIndex] = downloadedBlock
			numOfBlocksDownloaded += 1
		}
	}

	return &DownloadedPiece{
		Data:  piece.AssembleBlocks(downloadedBlocks),
		Piece: piece,
	}, nil
}

func Download(torrentFile string, dest string) error {
	trrnt, err := torrent.NewTorrent(torrentFile)

	if err != nil {
		return err
	}

	peers, err := trrnt.GetPeers()

	if err != nil {
		return err
	}

	numOfPiecesDownloaded := 0
	numOfPiecesToDownload := len(trrnt.Info.Pieces)

	requestBatchSize := len(peers)
	downloadedPieces := make(chan DownloadedPiece, requestBatchSize)

	tempDir, err := os.MkdirTemp("", trrnt.Info.Name)

	if err != nil {
		return err
	}

	defer os.RemoveAll(tempDir)

	for numOfPiecesDownloaded < numOfPiecesToDownload {
		pendingPieces := trrnt.Info.Pieces[numOfPiecesDownloaded:]
		numOfPendingPieces := len(pendingPieces)
		currentBatchSize := min(requestBatchSize, numOfPendingPieces)

		for i := 0; i < currentBatchSize; i++ {
			go func(piece torrent.Piece, peer torrent.Peer, infoHash [sha1.Size]byte) {
				downloadedPiece, err := DownloadPiece(piece, peer, infoHash)

				if err != nil {
					downloadedPieces <- DownloadedPiece{Err: err}
				}

				downloadedPieces <- *downloadedPiece

				// todo: verify hash of downloaded piece
				return
			}(pendingPieces[i], peers[i], trrnt.InfoHash)
		}

		for i := 0; i < currentBatchSize; i++ {
			downloadedPiece := <-downloadedPieces

			if downloadedPiece.Err != nil {
				return err
			}

			file := filepath.Join(tempDir, fmt.Sprintf("%d.piece", downloadedPiece.Piece.Index))

			if err := os.WriteFile(file, downloadedPiece.Data, 0666); err != nil {
				return err
			}

			numOfPiecesDownloaded += 1
		}

		if err := utils.MergeDirectoryToFile(tempDir, dest); err != nil {
			return err
		}
	}
	return nil
}
