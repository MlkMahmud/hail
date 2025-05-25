package torrent

import (
	"bytes"
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"log"
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

	pendingRequests   map[string]*messageRequest
	pendingRequestsMu sync.Mutex
	reader            *messageReader
	writer            *messageWriter
}

type peerConnectionInitConfig struct {
	bitfieldSize int
	dialTimeout  time.Duration
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

func (p *peerConnection) initiateHandshake() error {
	if err := p.sendHandshakeRequest(); err != nil {
		return err
	}

	if err := p.receiveHandshakeResponse(); err != nil {
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

func (p *peerConnection) receiveHandshakeResponse() error {
	responseBuffer := make([]byte, handshakeMessageLen)

	// Use the reader's read method to read the exact number of bytes
	if err := p.reader.readBuffer(responseBuffer); err != nil {
		return fmt.Errorf("failed to receive base handshake response: %w", err)
	}

	// Validate the protocol string length
	if responseBuffer[0] != byte(pstrLen) {
		return fmt.Errorf("expected protocol string length to be %d, but got %d", pstrLen, responseBuffer[0])
	}

	// Validate the protocol string
	if string(responseBuffer[1:pstrLen+1]) != pstr {
		return fmt.Errorf("expected protocol string to be '%s', but got '%s'", pstr, string(responseBuffer[1:pstrLen+1]))
	}

	// Validate the info hash
	if !bytes.Equal(responseBuffer[28:48], p.infoHash[:]) {
		return fmt.Errorf("received info hash does not match expected info hash")
	}

	// Extract the peer ID
	copy(p.remotePeerId[:], responseBuffer[48:])

	// Check for extension support
	if responseBuffer[25]&0x10 != 0 {
		p.supportsExtensions = true
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
		return nil, fmt.Errorf("received incorrect message type from peer: got \"%s\", expected \"%s\"", receivedMessageId, id)
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

func (p *peerConnection) sendHandshakeRequest() error {
	if p.writer == nil {
		return fmt.Errorf("cannot send base handshake: writer is not initialized")
	}

	messageBuffer := make([]byte, handshakeMessageLen)

	// Write the protocol string length
	messageBuffer[0] = byte(pstrLen)

	// Write the protocol string
	index := 1
	index += copy(messageBuffer[index:], []byte(pstr))

	// Write the reserved bytes (set to 0, except for extension support)
	index += copy(messageBuffer[index:], make([]byte, 5))
	messageBuffer[index] = byte(0x10) // Indicate support for extensions
	index += 1

	index += copy(messageBuffer[index:], make([]byte, 2))

	// Write the info hash
	index += copy(messageBuffer[index:], p.infoHash[:])

	// Write the peer ID
	index += copy(messageBuffer[index:], p.peerId[:])

	// Use the writer to send the handshake message
	if err := p.writer.writeBuffer(messageBuffer, time.Now().Add(time.Second*5)); err != nil {
		return fmt.Errorf("failed to send base handshake message: %w", err)
	}

	return nil
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

func (p *peerConnection) sendMessageRequest(key string, msg message) (*messageRequest, error) {
	p.pendingRequestsMu.Lock()
	defer p.pendingRequestsMu.Unlock()

	if p.pendingRequests == nil {
		return nil, fmt.Errorf("pending requests map is not initialized")
	}

	req := messageRequest{
		errorCh:    make(chan error, 1),
		responseCh: make(chan message, 1),
	}

	p.pendingRequests[key] = &req
	p.writer.messages <- msg

	return &req, nil
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

	msg := message{id: extensionMessageId, payload: messagePayloadBuffer}
	requestKey := "ext-handshake-request"

	msqReq, err := p.sendMessageRequest(requestKey, msg)

	if err != nil {
		return err
	}

	if reqErr := <-msqReq.errorCh; reqErr != nil {
		return reqErr
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

		for i := range currentBatchSize {
			go p.downloadBlock(pendingBlocks[i], resultsQueue, &mutex)
		}

		for range currentBatchSize {
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

func (p *peerConnection) handleIncomingMessage(msg message) error {
	return nil
}

func (p *peerConnection) initConnection(config peerConnectionInitConfig) error {
	if p.conn != nil {
		return nil
	}

	conn, err := net.DialTimeout("tcp", p.remotePeerAddress, config.dialTimeout)

	if err != nil {
		return fmt.Errorf("failed to initialized peer connection: %w", err)
	}

	p.conn = conn
	p.bitfield = make([]bool, config.bitfieldSize)
	messageBufferSize := 10

	p.reader = newMessageReader(messageReaderOpts{
		conn:              p.conn,
		messageBufferSize: messageBufferSize,
	})

	p.writer = newMessageWriter(messageWriterOpts{
		conn:              p.conn,
		messageBufferSize: messageBufferSize,
	})

	if err := p.initiateHandshake(); err != nil {
		return err
	}

	go p.reader.run()
	go p.writer.run()

	go func() {
		for {
			select {
			case msg := <-p.reader.messages:
				if err := p.handleIncomingMessage(msg); err != nil {
					log.Println(err)
					return
				}

			case readerErr := <-p.reader.errCh:
				log.Println(readerErr)
				return

			case writerErr := <-p.writer.errCh:
				log.Println(writerErr)
				return
			}
		}
	}()

	if err := p.sendExtensionHandshakeMessage(); err != nil {
		return err
	}

	return nil
}
