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
	metadataSize       int
	peerId             [20]byte
	peerExtensions     map[extensionName]uint8
	remotePeerAddress  string
	remotePeerId       [20]byte
	supportsExtensions bool
	unChoked           bool

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
		pendingRequests: map[string]*messageRequest{},
	}
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

func (p *peerConnection) deleteRequest(key string) {
	if p.pendingRequests == nil {
		return
	}

	p.pendingRequestsMu.Lock()
	defer p.pendingRequestsMu.Unlock()

	if _, ok := p.pendingRequests[key]; ok {
		delete(p.pendingRequests, key)
	}

	return
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

// todo: move to Torrent struct?
func (p *peerConnection) downloadMetadata() ([]byte, error) {
	if p.metadataSize == 0 {
		return nil, fmt.Errorf("metadata size is not set; cannot download metadata")
	}

	buffer := []byte{}
	downloaded := 0
	index := 0

	for downloaded < p.metadataSize {
		metadataPiece, err := p.sendMetadataMessageRequest(index)

		if err != nil {
			return nil, err
		}

		buffer = append(buffer, metadataPiece...)
		downloaded += len(metadataPiece)
		index += 1
	}

	return buffer, nil
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

func (p *peerConnection) handleExtensionHandshakeMessage(payload []byte) error {
	var err error

	defer func() {
		key := "ext-handshake-request"
		if req, ok := p.pendingRequests[key]; ok {
			req.errorCh <- err
			p.deleteRequest(key)
		}
	}()

	if len(payload) < 2 {
		err = fmt.Errorf("extension handshake message payload too short: got %d bytes", len(payload))
		return err
	}

	decodedPayload, _, decodeErr := bencode.DecodeValue(payload[1:])
	if decodeErr != nil {
		err = fmt.Errorf("failed to decode extension handshake message payload: %w", decodeErr)
		return err
	}

	dict, ok := decodedPayload.(map[string]any)
	if !ok {
		err = fmt.Errorf("expected decoded payload to be a dictionary, but received type %T: %v", decodedPayload, decodedPayload)
		return err
	}

	extensionsMap, ok := dict["m"].(map[string]any)
	if !ok {
		err = fmt.Errorf("expected decoded payload to include an \"m\" key mapping to a dictionary of supported extensions, but got type %T: %v", dict["m"], dict["m"])
		return err
	}

	extensions := make(map[extensionName]uint8)

	// Parse ut_metadata extension id
	value, ok := extensionsMap[string(utMetadata)]
	if ok {
		id, ok := value.(int)
		if !ok {
			err = fmt.Errorf("expected \"%s\" extension Id to be an integer, but received type %T: %v", utMetadata, value, value)
			return err
		}
		if id < 0 || id > math.MaxUint8 {
			err = fmt.Errorf("expected \"%s\" extension Id to be between 0 and 255, but received %d", utMetadata, id)
			return err
		}
		extensions[utMetadata] = uint8(id)
	}

	p.peerExtensions = extensions

	// Parse metadata_size if present
	if metadataSizeVal, ok := dict["metadata_size"]; ok {
		switch v := metadataSizeVal.(type) {
		case int:
			p.metadataSize = v
		default:
			err = fmt.Errorf("expected \"metadata_size\" to be an integer, but received type %T: %v", metadataSizeVal, metadataSizeVal)
			return err
		}
	}

	err = nil
	return nil
}

func (p *peerConnection) handleExtensionMessage(msg message) error {
	if msg.id != extensionMessageId {
		return fmt.Errorf("received non-extension message \"%s\"", msg.id)
	}

	extendedMsgId := msg.payload[0]

	switch extendedMsgId {
	case byte(extMsgRequest):
		return p.handleExtensionHandshakeMessage(msg.payload)
	case byte(utMetadataId):
		return p.handleMetadataExtensionMessage(msg.payload)
	default:
		log.Printf("received unknown extension message id: %d", extendedMsgId)
		return nil
	}
}

func (p *peerConnection) handleMetadataExtensionMessage(payload []byte) error {
	var err error
	var key string
	var response []byte

	defer func() {
		if key == "" {
			return
		}

		if req, ok := p.pendingRequests[key]; ok {
			if err != nil {
				req.errorCh <- err
				close(req.errorCh)
			} else {
				req.responseCh <- response
				close(req.responseCh)
			}
			p.deleteRequest(key)
		}
	}()

	if len(payload) == 0 {
		err = fmt.Errorf("metadata response payload is empty")
		return err
	}

	if receivedId := int(payload[0]); receivedId != int(utMetadataId) {
		err = fmt.Errorf("expected \"%s\" extension Id to be %d, but received %d", utMetadata, utMetadataId, receivedId)
		return err
	}

	decoded, nextCharIndex, decodeErr := bencode.DecodeValue(payload[1:])
	if decodeErr != nil {
		err = fmt.Errorf("failed to decode metadata response payload: %w", decodeErr)
		return err
	}

	dict, ok := decoded.(map[string]any)
	if !ok {
		err = fmt.Errorf("expected decoded metadata response to be a dictionary, but received %v", dict)
		return err
	}

	// Parse piece index for request key
	pieceIndex, ok := dict["piece"].(int)
	if !ok {
		err = fmt.Errorf("expected \"piece\" key to be an integer, but received %v", dict["piece"])
		return err
	}

	key = fmt.Sprintf("metadata-piece-request-%d", pieceIndex)

	if dict["msg_type"] == int(extMsgReject) {
		err = fmt.Errorf("peer does not have the piece of metadata that was requested")
		return err
	}

	if dict["msg_type"] != int(extMsgData) {
		err = fmt.Errorf("expected \"msg_type\" key to have value %d, but got %v", int(extMsgData), dict["msg_type"])
		return err
	}

	pieceSize, ok := dict["total_size"].(int)
	if !ok {
		err = fmt.Errorf("expected \"total_size\" key to be an integer, but received %v", dict["total_size"])
		return err
	}

	metadataPieceStartIndex := nextCharIndex + 1 // add one to account for the first byte (the extension Id)
	metadataPiece := payload[metadataPieceStartIndex:]

	if receivedPieceSize := len(metadataPiece); receivedPieceSize != pieceSize {
		err = fmt.Errorf("expected metadata piece to have length %d, but received %d", pieceSize, receivedPieceSize)
		return err
	}

	response = metadataPiece
	return nil
}

func (p *peerConnection) handleIncomingMessage(msg message) error {
	var err error

	switch msg.id {
	case bitfieldMessageId:
		err = p.parseBitFieldMessage(msg)

	case choke:
		p.unChoked = false

	case extensionMessageId:
		err = p.handleExtensionMessage(msg)

	case unchokeMessageId:
		p.unChoked = true

	default:
		err = fmt.Errorf("received unknown message id: %v", msg.id)
	}

	return err
}

func (p *peerConnection) hasPiece(pieceIndex int) bool {
	if pieceIndex < 0 || pieceIndex >= len(p.bitfield) {
		return false
	}

	return p.bitfield[pieceIndex]
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

func (p *peerConnection) parseBitFieldMessage(msg message) error {
	if msg.id != bitfieldMessageId {
		return fmt.Errorf("expected message id to be '%s', but got '%s'", bitfieldMessageId, msg.id)
	}

	numOfPieces := len(p.bitfield)
	expectedBitFieldLength := int(math.Ceil(float64(numOfPieces) / byteSize))

	if numOfPieces == 0 {
		return nil
	}

	if receivedBitfieldLength := len(msg.payload); receivedBitfieldLength != expectedBitFieldLength {
		return fmt.Errorf("expected 'Bitfield' payload to contain '%d' bytes, but got '%d'", expectedBitFieldLength, receivedBitfieldLength)
	}

	for index := range numOfPieces {
		byteArrayIndex := index / byteSize
		byteIndex := index % byteSize
		// In an 8-bit number, the MSB (bit 7) has a place value of 2⁷,
		placeValue := 7 - byteIndex
		mask := int(math.Pow(float64(2), float64(placeValue)))

		isBitSet := (msg.payload[byteArrayIndex] & byte(mask)) != 0
		p.bitfield[index] = isBitSet
	}

	return nil
}

func (p *peerConnection) receiveHandshakeResponse() error {
	if p.reader == nil {
		return fmt.Errorf("cannot read base handshake: reader is not initialized")
	}

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

	if err := p.writer.writeBuffer(messageBuffer, time.Now().Add(time.Second*5)); err != nil {
		return fmt.Errorf("failed to send base handshake message: %w", err)
	}

	return nil
}

func (p *peerConnection) sendInterestAndAwaitUnchokeMessage() error {
	if p.unChoked {
		return nil
	}

	if err := p.sendMessage(interestedMessageId, nil); err != nil {
		return fmt.Errorf("failed to send 'Interested' message to peer: %w", err)
	}

	if _, err := p.receiveMessage(unchokeMessageId); err != nil {
		return fmt.Errorf("failed to receive 'Unchoke' message from peer: %w", err)
	}

	p.unChoked = true

	return nil
}

// todo: add metadata size to request payload if you have it.
func (p *peerConnection) sendExtensionHandshakeMessage() error {
	bencodedString, err := bencode.EncodeValue(map[string]any{
		"m": map[string]any{
			string(utMetadata): int(utMetadataId),
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

func (p *peerConnection) sendMessageRequest(key string, msg message) (*messageRequest, error) {
	p.pendingRequestsMu.Lock()
	defer p.pendingRequestsMu.Unlock()

	if p.pendingRequests == nil {
		return nil, fmt.Errorf("pending requests map is not initialized")
	}

	req := messageRequest{
		errorCh:    make(chan error, 1),
		responseCh: make(chan []byte, 1),
	}

	p.pendingRequests[key] = &req
	p.writer.messages <- msg

	return &req, nil
}

func (p *peerConnection) sendMetadataMessageRequest(pieceIndex int) ([]byte, error) {
	key := fmt.Sprintf("metadata-piece-request-%d", pieceIndex)
	defer p.deleteRequest(key)

	bencodedString, err := bencode.EncodeValue(map[string]any{
		"msg_type": int(extMsgRequest),
		"piece":    pieceIndex,
	})

	if err != nil {
		return nil, fmt.Errorf("failed to encode metadata extension request payload %w", err)
	}

	extensionMessageIdLength := 1
	messagePayloadBuffer := make([]byte, extensionMessageIdLength+len(bencodedString))

	index := 0
	messagePayloadBuffer[index] = byte(p.peerExtensions[utMetadata])

	index += 1
	copy(messagePayloadBuffer[index:], []byte(bencodedString))

	req, err := p.sendMessageRequest(key, message{id: extensionMessageId, payload: messagePayloadBuffer})

	if err != nil {
		return nil, fmt.Errorf("failed to send metadata piece request: %w", err)
	}

	select {
	case response := <-req.responseCh:
		return response, nil

	case err := <-req.errorCh:
		return nil, err

	case <-time.After(10 * time.Second):
		return nil, fmt.Errorf("timed out waiting for metadata piece response from peer")
	}
}

// Checks if the peer supports a specific extension.
func (p *peerConnection) supportsExtension(ext extensionName) bool {
	extId, ok := p.peerExtensions[ext]

	return ok && extId != 0
}
