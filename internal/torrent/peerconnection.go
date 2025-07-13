package torrent

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"log/slog"
	"math"
	"net"
	"sync"
	"time"

	"github.com/MlkMahmud/hail/internal/bencode"
)

type blockRequestResult struct {
	block block
	err   error
}

type peerConnection struct {
	bitfield           []bool
	bitfieldReadyCh    chan struct{}
	closeCh            chan struct{}
	conn               net.Conn
	failedAttempts     int
	infoHash           [sha1.Size]byte
	logger             *slog.Logger
	metadataSize       int
	peerExtensions     map[extensionName]uint8
	peerId             [20]byte
	pendingRequests    map[string]*messageRequest
	pendingRequestsMu  sync.Mutex
	remotePeerSocket   string
	remotePeerId       [20]byte
	reader             *messageReader
	supportsExtensions bool
	unChoked           bool
	writer             *messageWriter
}

type peerConnectionInitConfig struct {
	bitfieldSize int
	dialTimeout  time.Duration
}

type peerConnectionOpts struct {
	infoHash         [sha1.Size]byte
	logger           *slog.Logger
	peerId           [20]byte
	remotePeerSocket string
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
		infoHash:         opts.infoHash,
		logger:           opts.logger,
		remotePeerSocket: opts.remotePeerSocket,
		peerId:           opts.peerId,
		pendingRequests:  map[string]*messageRequest{},
	}
}

func (p *peerConnection) close() {
	if p.closeCh != nil {
		select {
		case <-p.closeCh:
			p.logger.Debug(fmt.Sprintf("[%s]: peer connection has been closed already", p.remotePeerSocket))

		default:
			close(p.closeCh)
		}
	}

	if p.conn != nil {
		p.conn.Close()
	}

	p.closeCh = nil
	p.conn = nil
}

func (p *peerConnection) deleteRequest(key string) {
	if p.pendingRequests == nil {
		return
	}

	p.pendingRequestsMu.Lock()
	defer p.pendingRequestsMu.Unlock()

	delete(p.pendingRequests, key)
}

func (p *peerConnection) downloadMetadata() ([]byte, error) {
	if p.metadataSize == 0 {
		return nil, fmt.Errorf("metadata size is not set; cannot download metadata")
	}

	buffer := []byte{}
	downloaded := 0
	index := 0

	for downloaded < p.metadataSize {
		metadataPiece, err := p.sendMetadataPieceRequest(index)

		if err != nil {
			return nil, err
		}

		buffer = append(buffer, metadataPiece...)
		downloaded += len(metadataPiece)
		index += 1
	}

	return buffer, nil
}

func (p *peerConnection) downloadPiece(piece piece) (*downloadedPiece, error) {
	if p.conn == nil {
		return nil, fmt.Errorf("peer connection has not been established")
	}

	if err := p.waitForBitfieldMessage(); err != nil {
		return nil, err
	}

	if !p.hasPiece(piece.index) {
		// todo: create custom error so caller can handle it differently
		return nil, fmt.Errorf("peer %s does not have piece at index %d", p.remotePeerSocket, piece.index)
	}

	if err := p.sendInterestedMessage(); err != nil {
		return nil, fmt.Errorf("failed to download piece at index %d: %w", piece.index, err)
	}

	blocks := piece.getBlocks()
	numOfBlocks := len(blocks)
	numOfBlocksDownloaded := 0

	downloadedBlocks := make([]block, numOfBlocks)
	maxBatchSize := 5
	resultsQueue := make(chan blockRequestResult)

	for numOfBlocksDownloaded < numOfBlocks {
		pendingBlocks := blocks[numOfBlocksDownloaded:]
		numOfPendingBlocks := len(pendingBlocks)
		currentBatchSize := min(numOfPendingBlocks, maxBatchSize)

		for i := range currentBatchSize {
			go p.sendRequestMessage(pendingBlocks[i], resultsQueue)
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

func (p *peerConnection) handleBitfieldMessage(msg message) error {
	if msg.id != bitfieldMessageId {
		return fmt.Errorf("expected message id to be '%s', but got '%s'", bitfieldMessageId, msg.id)
	}

	select {
	case <-p.bitfieldReadyCh:
		return fmt.Errorf("bitfield message already received for this connection")
	default:
	}

	defer func() {
		select {
		case <-p.bitfieldReadyCh:
		default:
			close(p.bitfieldReadyCh)
		}
	}()

	numOfPieces := len(p.bitfield)
	expectedBitFieldLength := int(math.Ceil(float64(numOfPieces) / byteSize))

	if numOfPieces == 0 {
		return nil
	}

	if msg.payload == nil {
		return fmt.Errorf("received nil payload for bitfield message")
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

func (p *peerConnection) handleHaveMessage(msg message) error {
	if msg.id != haveMessageId {
		return fmt.Errorf("expected message id to be '%s', but got '%s'", haveMessageId, msg.id)
	}

	if payloadLength := len(msg.payload); payloadLength != 4 {
		return fmt.Errorf("invalid 'have' message: expected payload length of 4 bytes, got %d", payloadLength)
	}

	pieceIndex := binary.BigEndian.Uint32(msg.payload)

	if bitfieldSize := len(p.bitfield); int(pieceIndex) >= bitfieldSize {
		return fmt.Errorf("invalid 'have' message: piece index %d out of range (bitfield size: %d)", pieceIndex, bitfieldSize)
	}

	p.bitfield[pieceIndex] = true

	return nil
}

func (p *peerConnection) handleExtensionHandshakeMessage(payload []byte) error {
	var err error

	key := "ext-handshake-request"
	defer p.respondToRequest(key, nil, err)

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
		p.logger.Debug(
			fmt.Sprintf("received unknown extension message id: %d", extendedMsgId),
		)
		return nil
	}
}

func (p *peerConnection) handleMetadataExtensionMessage(payload []byte) error {
	onExit := func(key string, response []byte, err error) {
		if key == "" {
			return
		}

		p.respondToRequest(key, response, err)
	}

	if len(payload) == 0 {
		onExit("", nil, nil)
		return fmt.Errorf("ut_metadata response payload is empty")
	}

	if receivedId := int(payload[0]); receivedId != int(utMetadataId) {
		onExit("", nil, nil)
		return fmt.Errorf("expected \"%s\" extension Id to be %d, but received %d", utMetadata, utMetadataId, receivedId)
	}

	decoded, nextCharIndex, decodeErr := bencode.DecodeValue(payload[1:])
	if decodeErr != nil {
		onExit("", nil, nil)
		return fmt.Errorf("failed to decode ut_metadata response payload: %w", decodeErr)
	}

	dict, ok := decoded.(map[string]any)
	if !ok {
		onExit("", nil, nil)
		return fmt.Errorf("expected decoded ut_metadata response to be a dictionary, but received %v", dict)
	}

	// Parse piece index for request key
	pieceIndex, ok := dict["piece"].(int)

	if !ok {
		onExit("", nil, nil)
		return fmt.Errorf("expected \"piece\" key to be an integer, but received %v", dict["piece"])
	}

	key := fmt.Sprintf("metadata-piece-request-%d", pieceIndex)

	if dict["msg_type"] == int(extMsgReject) {
		err := fmt.Errorf("peer does not have the piece of metadata that was requested")
		onExit(key, nil, err)
		return err
	}

	if dict["msg_type"] != int(extMsgData) {
		err := fmt.Errorf("expected \"msg_type\" key to have value %d, but got %v", int(extMsgData), dict["msg_type"])
		onExit(key, nil, err)
		return err
	}

	metadataPieceStartIndex := nextCharIndex + 1 // add one to account for the first byte (the extension Id)
	metadataPiece := payload[metadataPieceStartIndex:]

	onExit(key, metadataPiece, nil)
	return nil
}

func (p *peerConnection) handleIncomingMessage(msg message) error {
	var err error

	switch msg.id {
	case bitfieldMessageId:
		err = p.handleBitfieldMessage(msg)

	case choke:
		p.unChoked = false

	case extensionMessageId:
		err = p.handleExtensionMessage(msg)

	case haveMessageId:
		err = p.handleHaveMessage(msg)

	case pieceMessageId:
		err = p.handlePieceMessage(msg)

	case unchokeMessageId:
		err = p.handleUnChokeMessage(msg)

	default:
		err = fmt.Errorf("received unknown message id: %v", msg.id)
	}

	return err
}

func (p *peerConnection) handlePieceMessage(msg message) error {
	var err error

	if msg.id != pieceMessageId {
		return fmt.Errorf("expected message id to be '%s', but got '%s'", pieceMessageId, msg.id)
	}

	if len(msg.payload) < 9 {
		err = fmt.Errorf("piece message payload too short: expected at least 9 bytes, got %d", len(msg.payload))
		return err
	}

	index := 0

	blockPieceIndex := binary.BigEndian.Uint32(msg.payload[index:])
	index += 4

	blockPieceOffset := binary.BigEndian.Uint32(msg.payload[index:])
	index += 4

	response := msg.payload[index:]
	blockLength := len(response)

	if blockLength > blockSize {
		err = fmt.Errorf("received block size %d exceeds maximum allowed length %d", blockLength, blockSize)
	}

	key := fmt.Sprintf("piece-%d-offset-%d-length-%d", blockPieceIndex, blockPieceOffset, blockLength)
	p.respondToRequest(key, response, err)

	return err
}

func (p *peerConnection) handleUnChokeMessage(msg message) error {
	if msg.id != unchokeMessageId {
		return fmt.Errorf("expected message id to be '%s', but got '%s'", unchokeMessageId, msg.id)
	}

	p.unChoked = true
	p.respondToRequest("interested", nil, nil)

	return nil
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

	conn, err := net.DialTimeout("tcp", p.remotePeerSocket, config.dialTimeout)

	if err != nil {
		return fmt.Errorf("failed to initialize peer connection: %w", err)
	}

	p.conn = conn
	p.bitfield = make([]bool, config.bitfieldSize)
	p.bitfieldReadyCh = make(chan struct{}, 1)
	p.closeCh = make(chan struct{}, 1)

	messageBufferSize := 10

	p.reader = newMessageReader(messageReaderOpts{
		conn:              p.conn,
		messageBufferSize: messageBufferSize,
	})

	p.writer = newMessageWriter(messageWriterOpts{
		conn:              p.conn,
		messageBufferSize: messageBufferSize,
	})

	ctx, cancelFunc := context.WithCancel(context.Background())

	shutDownFn := func(err error) {
		if err != nil {
			p.logger.Debug(err.Error())
		}

		cancelFunc()
	}

	if err := p.initiateHandshake(); err != nil {
		shutDownFn(err)
		return err
	}

	if err := p.startMessageReaderAndWriter(ctx); err != nil {
		shutDownFn(err)
		return err
	}

	go p.startConnectionListener(shutDownFn)

	if err := p.sendExtensionHandshake(); err != nil {
		shutDownFn(err)
		return err
	}

	return nil
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

func (p *peerConnection) respondToRequest(key string, response []byte, err error) {
	p.pendingRequestsMu.Lock()
	defer p.pendingRequestsMu.Unlock()

	if req, ok := p.pendingRequests[key]; ok {
		if err != nil {
			req.errorCh <- err
		} else {
			req.responseCh <- response
		}
	}
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

func (p *peerConnection) sendRequestMessage(blk block, resultsQueue chan blockRequestResult) {
	retries := 2

	var blockData []byte
	var mainError error

	key := fmt.Sprintf("piece-%d-offset-%d-length-%d", blk.pieceIndex, blk.begin, blk.length)
	payload := generateBlockRequestPayload(blk)

	defer p.deleteRequest(key)

	for range retries {
		req, err := p.sendMessage(key, message{id: requestMessageId, payload: payload})

		if err != nil {
			mainError = err
			continue
		}

		select {
		case err := <-req.errorCh:
			mainError = fmt.Errorf("failed to send 'Request' message to peer at address %s for block (pieceIndex: %d, begin: %d, length: %d): %w", p.remotePeerSocket, blk.pieceIndex, blk.begin, blk.length, err)
			continue

		case <-time.After(5 * time.Second):
			mainError = fmt.Errorf("timed out waiting for block response from peer at address %s for block (pieceIndex: %d, begin: %d, length: %d)", p.remotePeerSocket, blk.pieceIndex, blk.begin, blk.length)
			continue

		case data := <-req.responseCh:
			if dataLength := len(data); dataLength != blk.length {
				mainError = fmt.Errorf("received block data length (%d) does not match requested length (%d)", dataLength, blk.length)
				continue
			}

			mainError = nil
			blockData = data
		}

		break
	}

	resultsQueue <- blockRequestResult{
		block: block{
			begin:      blk.begin,
			data:       blockData,
			length:     blk.length,
			pieceIndex: blk.pieceIndex,
		},
		err: mainError,
	}
}

func (p *peerConnection) sendExtensionHandshake() error {
	if !p.supportsExtensions {
		return nil
	}

	requestKey := "ext-handshake-request"
	defer p.deleteRequest(requestKey)

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

	req, err := p.sendMessage(requestKey, msg)

	if err != nil {
		return err
	}

	select {
	case err := <-req.errorCh:
		return err
	case <-req.responseCh:
		return nil
	case <-time.After(5 * time.Second):
		return fmt.Errorf("timed out waiting for extension handshake response from peer: %s", p.remotePeerSocket)
	}
}

func (p *peerConnection) sendInterestedMessage() error {
	if p.unChoked {
		return nil
	}

	key := "interested"
	defer p.deleteRequest(key)

	req, err := p.sendMessage(key, message{id: interestedMessageId, payload: nil})

	if err != nil {
		return fmt.Errorf("failed to send \"%s\" message: %w", interestedMessageId, err)
	}

	select {
	case err := <-req.errorCh:
		return err
	case <-req.responseCh:
		return nil
	case <-time.After(5 * time.Second):
		return fmt.Errorf("timed out waiting for \"%s\" message from peer: %s", unchokeMessageId, p.remotePeerSocket)
	}
}

func (p *peerConnection) sendMessage(key string, msg message) (*messageRequest, error) {
	if p.pendingRequests == nil {
		return nil, fmt.Errorf("pending requests map is not initialized")
	}

	if p.writer == nil {
		return nil, fmt.Errorf("peer connection writer has not been initialized")
	}

	if p.closeCh == nil {
		return nil, fmt.Errorf("peer connection has not been successfully initialized")
	}

	p.pendingRequestsMu.Lock()
	defer p.pendingRequestsMu.Unlock()

	var req *messageRequest

	if existingReq, ok := p.pendingRequests[key]; ok {
		req = existingReq
	} else {
		req = &messageRequest{errorCh: make(chan error, 1), responseCh: make(chan []byte, 1)}
		p.pendingRequests[key] = req
	}

	select {
	case <-p.closeCh:
		return nil, fmt.Errorf("peer connection has been closed")
	case p.writer.messages <- msg:
		return req, nil
	case <-time.After(5 * time.Second):
		return nil, fmt.Errorf("timed out waiting to send message to buffer")
	}
}

func (p *peerConnection) sendMetadataPieceRequest(pieceIndex int) ([]byte, error) {
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

	index := copy(messagePayloadBuffer, []byte{p.peerExtensions[utMetadata]})
	copy(messagePayloadBuffer[index:], []byte(bencodedString))

	req, err := p.sendMessage(key, message{id: extensionMessageId, payload: messagePayloadBuffer})

	if err != nil {
		return nil, fmt.Errorf("failed to send metadata piece request: %w", err)
	}

	select {
	case response := <-req.responseCh:
		return response, nil

	case err := <-req.errorCh:
		return nil, err

	case <-time.After(5 * time.Second):
		return nil, fmt.Errorf("timed out waiting for metadata piece response from peer")
	}
}

// Starts a loop that listens for incoming messages and errors from the peer connection's reader and writer.
//
// It invokes the provided exitFn callback with an error when the connection is closed, an error occurs, or the listener exits for any reason.
// The listener will exit if the reader or writer encounters an error, the close channel is closed, or the reader's message channel is closed unexpectedly.
func (p *peerConnection) startConnectionListener(exitFn func(err error)) {
	err := error(nil)
	exitLoop := false

	handleError := func(e error) {
		err = e
		exitLoop = true
	}

	for !exitLoop {
		select {
		case msg, ok := <-p.reader.messages:
			if !ok {
				handleError(fmt.Errorf("reader.messages channel closed unexpectedly"))
			}

			if e := p.handleIncomingMessage(msg); e != nil {
				handleError(e)
			}

		case e := <-p.reader.errCh:
			handleError(e)

		case e := <-p.writer.errCh:
			handleError(e)

		case <-p.closeCh:
			handleError(nil)
		}
	}

	exitFn(err)
}

// Starts the message reader and writer goroutines for the peer connection.
// It launches the reader and writer using the provided context, allowing them to be cancelled when the context is done.
// Returns an error if either the reader or writer is not initialized.
func (p *peerConnection) startMessageReaderAndWriter(ctx context.Context) error {
	if p.reader == nil {
		return fmt.Errorf("cannot start message reader: reader is not initialized")
	}

	if p.writer == nil {
		return fmt.Errorf("cannot start message writer: writer is not initialized")
	}

	go p.reader.run(ctx)
	go p.writer.run(ctx)

	return nil
}

// Checks if the peer supports a specific extension.
func (p *peerConnection) supportsExtension(ext extensionName) bool {
	extId, ok := p.peerExtensions[ext]

	return ok && extId != 0
}

func (p *peerConnection) waitForBitfieldMessage() error {
	if p.bitfieldReadyCh == nil {
		return fmt.Errorf("bitfield channel has not been initialized")
	}

	select {
	case <-p.bitfieldReadyCh:
		return nil

	case <-time.After(5 * time.Second):
		return fmt.Errorf("timed out waiting for bitfield message")
	}
}
