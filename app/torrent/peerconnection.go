package torrent

import (
	"bytes"
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"math"
	"net"
	"sync"
	"time"

	"github.com/codecrafters-io/bittorrent-starter-go/app/bencode"
	"github.com/codecrafters-io/bittorrent-starter-go/app/utils"
)

type BlockRequestResult struct {
	block Block
	err   error
}

type PeerConnection struct {
	Conn               net.Conn
	FailedAttempts     int
	InfoHash           [sha1.Size]byte
	PeerAddress        string
	PeerId             string
	PeerExtensions     map[Extension]uint8
	SupportsExtensions bool
	Unchoked           bool
}

type PeerConnectionConfig struct {
	Peer Peer
}

type ReadWriteMutex struct {
	writer sync.Mutex
	reader sync.Mutex
}

const (
	handshakeMessageLen = pstrLen + 49
	pstr                = "BitTorrent protocol"
	pstrLen             = len(pstr)

	metadataExtensionId = 1
)

const (
	MaxFailedAttempts = 2
)

func NewPeerConnection(config PeerConnectionConfig) *PeerConnection {
	return &PeerConnection{
		InfoHash:    config.Peer.InfoHash,
		PeerAddress: fmt.Sprintf("%s:%d", config.Peer.IpAddress, config.Peer.Port),
	}
}

func generateBlockRequestPayload(block Block) []byte {
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

func (p *PeerConnection) completeBaseHandshake() error {
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
		return fmt.Errorf("failed to send base handshake message: %w", err)
	}

	responseBuffer := make([]byte, handshakeMessageLen)

	if _, err := utils.ConnReadFull(p.Conn, responseBuffer); err != nil {
		return fmt.Errorf("failed to receive base handshake response: %w", err)
	}

	responseLength := len(responseBuffer)

	if responseLength != handshakeMessageLen {
		return fmt.Errorf("expected handshake response message length to be '%d' long, but got '%d'", handshakeMessageLen, responseLength)
	}

	if receivedPstrLen := responseBuffer[0]; receivedPstrLen != byte(pstrLen) {
		return fmt.Errorf("expected handshake protocol string length to be '%d', but got '%v'", pstrLen, receivedPstrLen)
	}

	if receivedPstr := responseBuffer[1 : pstrLen+1]; string(receivedPstr) != pstr {
		return fmt.Errorf("expected protocol string to equal '%s', but got '%s'", pstr, receivedPstr)
	}

	if receivedInfoHash := responseBuffer[28:48]; !bytes.Equal(receivedInfoHash, p.InfoHash[:]) {
		return fmt.Errorf("received info hash %v does not match expected info hash %v", receivedInfoHash, p.InfoHash)
	}

	//The bit selected for the extension protocol is bit 20th from the right (counting starts at 0). So (reserved_byte[5] & 0x10) is the expression to use for checking if the client supports extended messaging.
	if reservedByteIndex := 25; bytes.Equal(responseBuffer[reservedByteIndex:reservedByteIndex+1], []byte{byte(0x10)}) {
		p.SupportsExtensions = true
	}

	peerIdStartIndex := 48
	p.PeerId = string(responseBuffer[peerIdStartIndex:])

	return nil
}

func (p *PeerConnection) completeExtensionHandshake() error {
	if err := p.sendExtensionHandshakeMessage(); err != nil {
		return err
	}

	if err := p.receiveExtensionHandshakeMessage(); err != nil {
		return err
	}

	return nil
}

func (p *PeerConnection) downloadBlock(block Block, resultsQueue chan<- BlockRequestResult, mutex *ReadWriteMutex) {
	retries := 2

	var downloadedBlock Block
	var mainError error

	for i := 0; i < retries; i++ {
		payload := generateBlockRequestPayload(block)

		mutex.writer.Lock()
		err := p.sendMessage(Request, payload)
		mutex.writer.Unlock()

		if err != nil {
			mainError = fmt.Errorf("failed to download block: %w", err)
			continue
		}

		mutex.reader.Lock()
		message, err := p.receiveMessage(PieceMessageId)
		mutex.reader.Unlock()

		if err != nil {
			mainError = fmt.Errorf("failed to download block: %w", err)
			continue
		}

		index := 0

		blockPieceIndex := binary.BigEndian.Uint32(message.Payload[index:])
		index += 4

		blockPieceOffset := binary.BigEndian.Uint32(message.Payload[index:])
		index += 4

		blockData := message.Payload[index:]

		downloadedBlock = Block{
			Begin:     int(blockPieceOffset),
			Data:      blockData,
			Length:    len(blockData),
			PiceIndex: int(blockPieceIndex),
		}

		break
	}

	resultsQueue <- BlockRequestResult{
		block: downloadedBlock,
		err:   mainError,
	}
}

func (p *PeerConnection) receiveExtensionHandshakeMessage() error {
	message, err := p.receiveMessage(ExtensionMessageId)

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

	p.PeerExtensions = extensions

	return nil
}

func (p *PeerConnection) receiveMessage(messageId MessageId) (*Message, error) {
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

func (p *PeerConnection) receiveMetadataMessage() ([]byte, error) {
	message, err := p.receiveMessage(ExtensionMessageId)

	if err != nil {
		return nil, fmt.Errorf("failed to receive metadata message: %w", err)
	}

	if len(message.Payload) == 0 {
		return nil, fmt.Errorf("metadata response payload is empty")
	}

	if receivedId := int(message.Payload[0]); receivedId != metadataExtensionId {
		return nil, fmt.Errorf("expected metadata extension Id to be %d, but received %d", metadataExtensionId, receivedId)
	}

	decoded, nextCharIndex, err := bencode.DecodeValue(message.Payload[1:])

	if err != nil {
		return nil, fmt.Errorf("failed to decode metadata response payload: %w", err)
	}

	dict, ok := decoded.(map[string]any)

	if !ok {
		return nil, fmt.Errorf("expected decoded metadata response to be a dictionary, but received %v", dict)
	}

	if dict["msg_type"] == int(ExtensionRejectMessageId) {
		return nil, fmt.Errorf("peer does not have the piece of metadata that was requested")
	}

	if dict["msg_type"] != int(ExtensionDataMessageId) {
		return nil, fmt.Errorf("expected \"msg_type\" key to have value %d, but got %v", int(ExtensionDataMessageId), dict["msg_type"])
	}

	pieceIndex, ok := dict["piece"].(int)

	if !ok {
		return nil, fmt.Errorf("expected \"piece\" key to be an integer, but received %v", pieceIndex)
	}

	pieceSize, ok := dict["total_size"].(int)

	if !ok {
		return nil, fmt.Errorf("expected \"total_size\" key to be an integer, but received %v", pieceSize)
	}

	metadataPieceStartIndex := nextCharIndex + 1 // add one to account for the first byte (the extension message Id)
	metadataPiece := message.Payload[metadataPieceStartIndex:]

	if receivedPieceSize := len(metadataPiece); receivedPieceSize != pieceSize {
		return nil, fmt.Errorf("expected metadata piece to have length %d, but received %d", pieceSize, receivedPieceSize)
	}

	return metadataPiece, nil
}

func (p *PeerConnection) sendInterestAndAwaitUnchokeMessage() error {
	if p.Unchoked {
		return nil
	}

	if err := p.sendMessage(Interested, nil); err != nil {
		return fmt.Errorf("failed to send 'Interested' message to peer: %w", err)
	}

	if _, err := p.receiveMessage(Unchoke); err != nil {
		return fmt.Errorf("failed to receive 'Unchoke' message from peer: %w", err)
	}

	p.Unchoked = true

	return nil
}

func (p *PeerConnection) sendExtensionHandshakeMessage() error {
	bencodedString, err := bencode.EncodeValue(map[string]any{
		"m": map[string]any{
			"ut_metadata": metadataExtensionId,
		},
	})

	if err != nil {
		return fmt.Errorf("failed to generate extension handshake payload: %w", err)
	}

	messagePayloadLength := len(bencodedString) + 1 // one extra byte for the extension message handshake Id (different from the message Id)
	messagePayloadBuffer := make([]byte, messagePayloadLength)

	index := 0

	messagePayloadBuffer[index] = byte(0) // write the extension message Id (0)
	index += 1

	copy(messagePayloadBuffer[index:], []byte(bencodedString))

	if err := p.sendMessage(ExtensionMessageId, messagePayloadBuffer); err != nil {
		return fmt.Errorf("failed to send extension handshake message: %w", err)
	}

	return nil
}

func (p *PeerConnection) sendMessage(messageId MessageId, payload []byte) error {
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

func (p *PeerConnection) sendMetadataRequestMessage(pieceIndex int) error {
	bencodedString, err := bencode.EncodeValue(map[string]any{
		"msg_type": int(ExtensionRequestMessageId),
		"piece":    pieceIndex,
	})

	if err != nil {
		return fmt.Errorf("failed to encode metadata extension request payload %w", err)
	}

	extensionMessageIdLength := 1
	messagePayloadBuffer := make([]byte, extensionMessageIdLength+len(bencodedString))

	index := 0
	messagePayloadBuffer[index] = byte(p.PeerExtensions[Metadata])

	index += 1
	copy(messagePayloadBuffer[index:], []byte(bencodedString))

	if err := p.sendMessage(ExtensionMessageId, messagePayloadBuffer); err != nil {
		return fmt.Errorf("failed to send metadata extension request %w", err)
	}

	return nil
}

func (p *PeerConnection) DownloadPiece(piece Piece) (*DownloadedPiece, error) {
	if err := p.InitConnection(); err != nil {
		return nil, fmt.Errorf("failed to initialize peer connection: %w", err)
	}

	if err := p.sendInterestAndAwaitUnchokeMessage(); err != nil {
		return nil, fmt.Errorf("failed to download piece at index %d: %w", piece.Index, err)
	}

	// todo: add a check to see if the remote peer for this connection has the piece we want to download
	blocks := piece.GetBlocks()
	numOfBlocks := len(blocks)
	numOfBlocksDownloaded := 0

	downloadedBlocks := make([]Block, numOfBlocks)
	maxBatchSize := 5
	mutex := ReadWriteMutex{}
	resultsQueue := make(chan BlockRequestResult)

	for numOfBlocksDownloaded < numOfBlocks {
		pendingBlocks := blocks[numOfBlocksDownloaded:]
		numOfPendingBlocks := len(pendingBlocks)
		currentBatchSize := min(numOfPendingBlocks, maxBatchSize)

		for i := 0; i < currentBatchSize; i++ {
			go p.downloadBlock(pendingBlocks[i], resultsQueue, &mutex)
		}

		for i := 0; i < currentBatchSize; i++ {
			result := <-resultsQueue

			if result.err != nil {
				return nil, fmt.Errorf("failed to download piece at index %d: %w", piece.Index, result.err)
			}

			downloadedBlock := result.block
			downloadedBlockIndex := downloadedBlock.Begin / BlockSize

			if downloadedBlockIndex >= numOfBlocks {

				return nil, fmt.Errorf("downloaded block offset %d is invalid", downloadedBlock.Begin)
			}

			downloadedBlocks[downloadedBlockIndex] = downloadedBlock
			numOfBlocksDownloaded += 1
		}

	}

	return &DownloadedPiece{
		Data:  piece.assembleBlocks(downloadedBlocks),
		Piece: piece,
	}, nil
}

func (p *PeerConnection) InitConnection() error {
	if p.Conn != nil {
		return nil
	}

	conn, err := net.DialTimeout("tcp", p.PeerAddress, 5*time.Second)

	if err != nil {
		return fmt.Errorf("failed to initialized peer connection: %w", err)
	}

	p.Conn = conn

	if err := p.completeBaseHandshake(); err != nil {
		return err
	}

	// todo: handle bitfield response
	if _, err := p.receiveMessage(Bitfield); err != nil {
		fmt.Printf("failed to receive 'Bitfield' message from peer: %v", err)
		return nil
	}

	if !p.SupportsExtensions {
		return nil
	}

	if err := p.completeExtensionHandshake(); err != nil {
		return err
	}

	return nil
}
