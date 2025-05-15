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

	"github.com/MlkMahmud/hail/bencode"
	"github.com/MlkMahmud/hail/utils"
)

type blockRequestResult struct {
	block block
	err   error
}

type peerConnection struct {
	bitfield           []bool
	conn               net.Conn
	failedAttempts     int
	hashFails          int
	infoHash           [sha1.Size]byte
	peerId             [20]byte
	peerExtensions     map[extension]uint8
	remotePeerAddress  string
	remotePeerId       [20]byte
	supportsExtensions bool
	unchoked           bool
}

type peerConnectionInitConfig struct {
	bitfieldSize int
}

type peerConnectionOpts struct {
	infoHash   [sha1.Size]byte
	peerId     [20]byte
	remotePeer peer
}

type readWriteMutex struct {
	writer sync.Mutex
	reader sync.Mutex
}

const (
	byteSize = 8
)

const (
	handshakeMessageLen = pstrLen + 49
	pstr                = "BitTorrent protocol"
	pstrLen             = len(pstr)

	metadataExtensionId = 1
)

const (
	maxHashFails                    = 3
	peerConnectionMaxFailedAttempts = 3
)

func newPeerConnection(opts peerConnectionOpts) *peerConnection {
	return &peerConnection{
		infoHash:          opts.infoHash,
		remotePeerAddress: fmt.Sprintf("%s:%d", opts.remotePeer.ipAddress, opts.remotePeer.port),
		peerId:            opts.peerId,
	}
}

func generateBlockRequestPayload(block block) []byte {
	blockBeginSize := 4
	blockIndexSize := 4
	blockLengthSize := 4
	messageBufferSize := blockBeginSize + blockIndexSize + blockLengthSize

	messageBuffer := make([]byte, messageBufferSize)

	index := 0

	binary.BigEndian.PutUint32(messageBuffer[index:], uint32(block.pieceIndex))
	index += blockIndexSize

	binary.BigEndian.PutUint32(messageBuffer[index:], uint32(block.begin))
	index += blockBeginSize

	binary.BigEndian.PutUint32(messageBuffer[index:], uint32(block.length))
	index += blockLengthSize

	return messageBuffer
}

func (p *peerConnection) completeBaseHandshake() error {
	messageBuffer := make([]byte, handshakeMessageLen)
	messageBuffer[0] = byte(pstrLen)

	index := 1
	index += copy(messageBuffer[index:], []byte(pstr))
	index += copy(messageBuffer[index:], make([]byte, 5))

	messageBuffer[index] = byte(16)
	index += 1

	index += copy(messageBuffer[index:], make([]byte, 2))
	index += copy(messageBuffer[index:], p.infoHash[:])
	index += copy(messageBuffer[index:], p.peerId[:])

	if _, err := utils.ConnWriteFull(p.conn, messageBuffer, 0); err != nil {
		return fmt.Errorf("failed to send base handshake message: %w", err)
	}

	responseBuffer := make([]byte, handshakeMessageLen)

	if _, err := utils.ConnReadFull(p.conn, responseBuffer, 0); err != nil {
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

	if receivedInfoHash := responseBuffer[28:48]; !bytes.Equal(receivedInfoHash, p.infoHash[:]) {
		return fmt.Errorf("received info hash %v does not match expected info hash %v", receivedInfoHash, p.infoHash)
	}

	//The bit selected for the extension protocol is bit 20th from the right (counting starts at 0). So (reserved_byte[5] & 0x10) is the expression to use for checking if the client supports extended messaging.
	if reservedByteIndex := 25; bytes.Equal(responseBuffer[reservedByteIndex:reservedByteIndex+1], []byte{byte(0x10)}) {
		p.supportsExtensions = true
	}

	peerIdStartIndex := 48
	remotePeerId := [20]byte{}
	copy(remotePeerId[:], responseBuffer[peerIdStartIndex:])

	p.remotePeerId = remotePeerId

	return nil
}

func (p *peerConnection) completeExtensionHandshake() error {
	if !p.supportsExtensions {
		return nil
	}

	if err := p.sendExtensionHandshakeMessage(); err != nil {
		return err
	}

	if err := p.receiveExtensionHandshakeMessage(); err != nil {
		return err
	}

	return nil
}

func (p *peerConnection) downloadBlock(requestedBlock block, resultsQueue chan<- blockRequestResult, mutex *readWriteMutex) {
	retries := 2

	var downloadedBlock block
	var mainError error

	for range retries {
		payload := generateBlockRequestPayload(requestedBlock)

		mutex.writer.Lock()
		err := p.sendMessage(requestMessageId, payload)
		mutex.writer.Unlock()

		if err != nil {
			mainError = fmt.Errorf("failed to send 'Request' message to peer at address %s for block (pieceIndex: %d, begin: %d, length: %d): %w", p.remotePeerAddress, requestedBlock.pieceIndex, requestedBlock.begin, requestedBlock.length, err)
			continue
		}

		mutex.reader.Lock()
		message, err := p.receiveMessage(pieceMessageId)
		mutex.reader.Unlock()

		if err != nil {
			mainError = fmt.Errorf("failed to receive 'Piece' message from peer: %w", err)
			continue
		}

		index := 0

		blockPieceIndex := binary.BigEndian.Uint32(message.payload[index:])
		index += 4

		blockPieceOffset := binary.BigEndian.Uint32(message.payload[index:])
		index += 4

		blockData := message.payload[index:]

		downloadedBlock = block{
			begin:      int(blockPieceOffset),
			data:       blockData,
			length:     len(blockData),
			pieceIndex: int(blockPieceIndex),
		}

		break
	}

	resultsQueue <- blockRequestResult{
		block: downloadedBlock,
		err:   mainError,
	}
}

func (p *peerConnection) hasPiece(pieceIndex int) bool {
	if pieceIndex < 0 || pieceIndex >= len(p.bitfield) {
		return false
	}

	return p.bitfield[pieceIndex]
}

func (p *peerConnection) downloadMetadata() ([]byte, error) {
	buffer := []byte{}
	hasDownloadedAllPieces := false
	index := 0

	for !hasDownloadedAllPieces {
		metadataPiece, err := p.downloadMetadataPiece(index)

		if err != nil {
			return nil, err
		}

		buffer = append(buffer, metadataPiece...)
		index += 1

		//  If it is not the last piece of the metadata, it MUST be 16kiB (blockSize).
		// todo: get metadata size from extension handshake request.
		if len(metadataPiece) != blockSize {
			hasDownloadedAllPieces = true
			break
		}
	}

	return buffer, nil
}

func (p *peerConnection) downloadMetadataPiece(pieceIndex int) ([]byte, error) {
	if err := p.sendMetadataRequestMessage(pieceIndex); err != nil {
		return nil, err
	}

	piece, err := p.receiveMetadataMessage()

	if err != nil {
		return nil, err
	}

	return piece, nil
}

func (p *peerConnection) parseBitFieldMessage() error {
	message, err := p.receiveMessage(bitfieldMessageId)

	if err != nil {
		return fmt.Errorf("failed to receive 'Bitfield' message from peer: %w", err)
	}

	numOfPieces := len(p.bitfield)
	expectedBitFieldLength := int(math.Ceil(float64(numOfPieces) / byteSize))

	if numOfPieces == 0 {
		return nil
	}

	if receivedBitfieldLength := len(message.payload); receivedBitfieldLength != expectedBitFieldLength {
		return fmt.Errorf("expected 'Bitfield' payload to contain '%d' bytes, but got '%d'", expectedBitFieldLength, receivedBitfieldLength)
	}

	for index := range numOfPieces {
		byteArrayIndex := index / byteSize
		byteIndex := index % byteSize
		// In an 8-bit number, the MSB (bit 7) has a place value of 2⁷,
		placeValue := 7 - byteIndex
		mask := int(math.Pow(float64(2), float64(placeValue)))

		isBitSet := (message.payload[byteArrayIndex] & byte(mask)) != 0
		p.bitfield[index] = isBitSet
	}

	return nil
}

func (p *peerConnection) receiveExtensionHandshakeMessage() error {
	message, err := p.receiveMessage(extensionMessageId)

	if err != nil {
		return fmt.Errorf("failed to receive extension handshake message %w", err)
	}

	// Ignore the first byte of the payload which contains the extension message ID.
	decodedPayload, _, err := bencode.DecodeValue(message.payload[1:])

	if err != nil {
		return fmt.Errorf("failed to decode extension handshake message payload %w", err)
	}

	dict, ok := decodedPayload.(map[string]any)

	if !ok {
		return fmt.Errorf("expected decoded payload to be a dictionary, but received %v", dict)
	}

	extensionsMap, ok := dict["m"].(map[string]any)
	extensions := make(map[extension]uint8)

	if !ok {
		return fmt.Errorf("expected decoded payload to include an \"m\" key which maps to a dictionary of supported extensions, but got %v", extensionsMap)
	}

	for _, ext := range []extension{metadataExt} {
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

	p.peerExtensions = extensions

	return nil
}

func (p *peerConnection) receiveMessage(id messageId) (*message, error) {
	messageLengthBuffer := make([]byte, 4)

	if _, err := utils.ConnReadFull(p.conn, messageLengthBuffer, 0); err != nil {
		return nil, err
	}

	messageLength := binary.BigEndian.Uint32(messageLengthBuffer)
	messageBuffer := make([]byte, messageLength)

	if _, err := utils.ConnReadFull(p.conn, messageBuffer, 0); err != nil {
		return nil, err
	}

	receivedMessageId := messageId(messageBuffer[0])

	if receivedMessageId != id {
		return nil, fmt.Errorf("expected received message Id to be %d, but got %d", id, receivedMessageId)
	}

	return &message{id: receivedMessageId, payload: messageBuffer[1:]}, nil
}

func (p *peerConnection) receiveMetadataMessage() ([]byte, error) {
	message, err := p.receiveMessage(extensionMessageId)

	if err != nil {
		return nil, fmt.Errorf("failed to receive metadata message: %w", err)
	}

	if len(message.payload) == 0 {
		return nil, fmt.Errorf("metadata response payload is empty")
	}

	if receivedId := int(message.payload[0]); receivedId != metadataExtensionId {
		return nil, fmt.Errorf("expected metadata extension Id to be %d, but received %d", metadataExtensionId, receivedId)
	}

	decoded, nextCharIndex, err := bencode.DecodeValue(message.payload[1:])

	if err != nil {
		return nil, fmt.Errorf("failed to decode metadata response payload: %w", err)
	}

	dict, ok := decoded.(map[string]any)

	if !ok {
		return nil, fmt.Errorf("expected decoded metadata response to be a dictionary, but received %v", dict)
	}

	if dict["msg_type"] == int(extensionRejectMessageId) {
		return nil, fmt.Errorf("peer does not have the piece of metadata that was requested")
	}

	if dict["msg_type"] != int(extensionDataMessageId) {
		return nil, fmt.Errorf("expected \"msg_type\" key to have value %d, but got %v", int(extensionDataMessageId), dict["msg_type"])
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
	metadataPiece := message.payload[metadataPieceStartIndex:]

	if receivedPieceSize := len(metadataPiece); receivedPieceSize != pieceSize {
		return nil, fmt.Errorf("expected metadata piece to have length %d, but received %d", pieceSize, receivedPieceSize)
	}

	return metadataPiece, nil
}

func (p *peerConnection) sendInterestAndAwaitUnchokeMessage() error {
	if p.unchoked {
		return nil
	}

	if err := p.sendMessage(interestedMessageId, nil); err != nil {
		return fmt.Errorf("failed to send 'Interested' message to peer: %w", err)
	}

	if _, err := p.receiveMessage(unchokeMessageId); err != nil {
		return fmt.Errorf("failed to receive 'Unchoke' message from peer: %w", err)
	}

	p.unchoked = true

	return nil
}

func (p *peerConnection) sendExtensionHandshakeMessage() error {
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

	if err := p.sendMessage(extensionMessageId, messagePayloadBuffer); err != nil {
		return fmt.Errorf("failed to send extension handshake message: %w", err)
	}

	return nil
}

func (p *peerConnection) sendMessage(messageId messageId, payload []byte) error {
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

	if _, err := utils.ConnWriteFull(p.conn, messageBuffer, 0); err != nil {
		return err
	}

	return nil
}

func (p *peerConnection) sendMetadataRequestMessage(pieceIndex int) error {
	bencodedString, err := bencode.EncodeValue(map[string]any{
		"msg_type": int(extensionRequestMessageId),
		"piece":    pieceIndex,
	})

	if err != nil {
		return fmt.Errorf("failed to encode metadata extension request payload %w", err)
	}

	extensionMessageIdLength := 1
	messagePayloadBuffer := make([]byte, extensionMessageIdLength+len(bencodedString))

	index := 0
	messagePayloadBuffer[index] = byte(p.peerExtensions[metadataExt])

	index += 1
	copy(messagePayloadBuffer[index:], []byte(bencodedString))

	if err := p.sendMessage(extensionMessageId, messagePayloadBuffer); err != nil {
		return fmt.Errorf("failed to send metadata extension request %w", err)
	}

	return nil
}

func (p *peerConnection) supportsExtension(ext extension) bool {
	_, ok := p.peerExtensions[ext]

	return ok
}

func (p *peerConnection) close() {
	if p.conn != nil {
		p.conn.Close()
	}
}

func (p *peerConnection) downloadPiece(piece piece) (*downloadedPiece, error) {
	if p.conn == nil {
		return nil, fmt.Errorf("peer connection has not been established")
	}

	if !p.hasPiece(piece.index) {
		// todo: should this be treated as an error?
		return nil, fmt.Errorf("peer %s does not have piece at index %d", p.remotePeerAddress, piece.index)
	}

	if err := p.sendInterestAndAwaitUnchokeMessage(); err != nil {
		return nil, fmt.Errorf("failed to download piece at index %d: %w", piece.index, err)
	}

	blocks := piece.getBlocks()
	numOfBlocks := len(blocks)
	numOfBlocksDownloaded := 0

	downloadedBlocks := make([]block, numOfBlocks)
	maxBatchSize := 5
	mutex := readWriteMutex{}
	resultsQueue := make(chan blockRequestResult)

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
				return nil, fmt.Errorf("failed to download piece at index %d: %w", piece.index, result.err)
			}

			downloadedBlock := result.block
			downloadedBlockIndex := downloadedBlock.begin / blockSize

			if downloadedBlockIndex >= numOfBlocks {
				return nil, fmt.Errorf("downloaded block offset %d is invalid", downloadedBlock.begin)
			}

			downloadedBlocks[downloadedBlockIndex] = downloadedBlock
			numOfBlocksDownloaded += 1
		}

	}

	return &downloadedPiece{
		data:  piece.assembleBlocks(downloadedBlocks),
		piece: piece,
	}, nil
}

func (p *peerConnection) initConnection(config peerConnectionInitConfig) error {
	if p.conn != nil {
		return nil
	}

	conn, err := net.DialTimeout("tcp", p.remotePeerAddress, 5*time.Second)

	if err != nil {
		return fmt.Errorf("failed to initialized peer connection: %w", err)
	}

	p.conn = conn
	p.bitfield = make([]bool, config.bitfieldSize)

	if err := p.completeBaseHandshake(); err != nil {
		return err
	}

	if err := p.parseBitFieldMessage(); err != nil {
		fmt.Printf("failed to receive 'Bitfield' message from peer: %v", err)
		return nil
	}

	if err := p.completeExtensionHandshake(); err != nil {
		return err
	}

	return nil
}
