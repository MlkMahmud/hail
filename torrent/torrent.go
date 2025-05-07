package torrent

import (
	"context"
	"crypto/sha1"
	"encoding/base32"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/MlkMahmud/hail/bencode"
	"github.com/MlkMahmud/hail/utils"
)

type file struct {
	torrent *Torrent

	Length int
	Name   string
	Offset int

	pieceEndIndex   int
	pieceStartIndex int
}

type torrentInfo struct {
	files  []file
	length int
	name   string
	pieces []Piece
}

type torrentStatus int

const (
	connecting torrentStatus = iota
	downloading
	finished
	seeding
)

const (
	maxPeers = 50
)

type Torrent struct {
	info     *torrentInfo
	infoHash [sha1.Size]byte

	ctx        context.Context
	cancelFunc context.CancelFunc

	bannedPeers        utils.Set
	bannedPeersCh      chan string
	failingPeers       map[string]Peer
	failingTrackers    utils.Set
	incomingPeersCh    chan []Peer
	maxPeerConnections int
	peerConnections    map[string]PeerConnection
	peers              map[string]Peer
	status             torrentStatus
	statusCh           chan torrentStatus
	trackers           utils.Set
}

// func downloadMetadataPiece(p PeerConnection, pieceIndex int) ([]byte, error) {
// 	if err := p.sendMetadataRequestMessage(pieceIndex); err != nil {
// 		return nil, err
// 	}

// 	piece, err := p.receiveMetadataMessage()

// 	if err != nil {
// 		return nil, err
// 	}

// 	return piece, nil
// }

func parseAnnounceList(list any) (*utils.Set, error) {
	trackers := utils.NewSet()

	announceList, ok := list.([]any)

	if !ok {
		return nil, fmt.Errorf("\"announce-list\" property should be a list, but received '%T'", announceList)
	}

	for listIndex, tier := range announceList {
		tierList, ok := tier.([]any)

		if !ok {
			return nil, fmt.Errorf("announce list contains an invalid entry at index %d", listIndex)
		}

		for tierIndex, url := range tierList {
			urlStr, ok := url.(string)

			if !ok {
				return nil, fmt.Errorf("announce list entry at index %d contains an invalid entry at index %d", listIndex, tierIndex)
			}

			if strings.HasPrefix(urlStr, "http://") || strings.HasPrefix(urlStr, "https://") || strings.HasPrefix(urlStr, "udp://") {
				trackers.Add(urlStr)
			}
		}
	}

	return trackers, nil
}

func parseFilesList(infoDict map[string]any, tr Torrent) (*torrentInfo, error) {
	filesList, ok := infoDict["files"].([]any)

	if !ok {
		return nil, fmt.Errorf("expected 'files' property to be a list, but received '%T'", filesList)
	}

	numOfFiles := len(filesList)
	files := make([]file, numOfFiles)

	pieceLength := infoDict["piece length"].(int)
	pieces := infoDict["pieces"].(string)
	piecesArr := []Piece{}

	fileOffset := 0
	piecesIndex := 0

	for i := range numOfFiles {
		entry, ok := filesList[i].(map[string]any)
		isLastFile := i == (numOfFiles - 1)

		if !ok {
			return nil, fmt.Errorf("files list contains an invalid entry at index '%d'", i)
		}

		if _, ok := entry["length"].(int); !ok {
			return nil, fmt.Errorf("files list entry at index '%d' contains an invalid 'length' property", i)
		}

		if _, ok := entry["path"].([]any); !ok {
			return nil, fmt.Errorf("files list entry at index '%d' contains an invalid 'path' property", i)
		}

		paths := entry["path"].([]any)
		pathList := make([]string, len(paths))

		for index, entry := range paths {
			if _, ok := entry.(string); !ok {
				return nil, fmt.Errorf("files list entry at index '%d' contains an invalid 'path' property", i)
			}

			pathList[index] = entry.(string)
		}

		fileLength := entry["length"].(int)
		path := filepath.Join(pathList...)

		pieceStartIndex := piecesIndex / sha1.Size
		pieceEndIndex := pieceStartIndex + (fileLength / pieceLength)

		result, err := parsePiecesHashes(fileLength, pieceLength, pieceStartIndex, pieces[piecesIndex:])

		if err != nil {
			return nil, fmt.Errorf("failed to parse files list entry at index '%d': %w", i, err)
		}

		files[i] = file{
			torrent:         &tr,
			Length:          fileLength,
			Name:            filepath.Join(infoDict["name"].(string), path),
			Offset:          fileOffset,
			pieceEndIndex:   pieceEndIndex,
			pieceStartIndex: pieceStartIndex,
		}
		/*
			If the offset for the next file is not '0' it means the final piece for this file was truncated.
			Given this assertion, we can copy all the parsed pieces except the last piece, seeing as it will be copied
			as the first piece for the next file, unless the current file is the last file in the list.
		*/
		if result.nextFileOffset != 0 && !isLastFile {
			piecesArr = append(piecesArr, result.pieces[:len(result.pieces)-1]...)
		} else {
			piecesArr = append(piecesArr, result.pieces...)
		}

		fileOffset = result.nextFileOffset
		piecesIndex += result.nextPieceStartIndex
	}

	return &torrentInfo{
		files:  files,
		pieces: piecesArr,
	}, nil
}

func parseInfoDict(infoDict map[string]any, tr Torrent) (*torrentInfo, error) {
	for key, value := range map[string]any{"name": "", "piece length": 0, "pieces": ""} {
		if _, exists := infoDict[key]; !exists {
			return nil, fmt.Errorf("metainfo 'info' dictionary is missing required property '%s'", key)
		}

		expectedType := reflect.TypeOf(value)
		receivedType := reflect.TypeOf(infoDict[key])

		if receivedType != expectedType {
			return nil, fmt.Errorf("expected the '%s' property to be of type '%v', but received '%v'", key, expectedType, receivedType)
		}
	}

	if _, ok := infoDict["files"]; ok {
		info, err := parseFilesList(infoDict, tr)

		return info, err
	}

	if _, ok := infoDict["length"]; !ok {
		return nil, fmt.Errorf("metainfo 'info' dictionary must contain a 'files' or 'length' property")
	}

	fileLength, ok := infoDict["length"].(int)

	if !ok {
		return nil, fmt.Errorf("'length' property of metainfo info dictionary must be an integer not %T", fileLength)
	}

	pieceLength := infoDict["piece length"].(int)
	pieceOffset := 0
	piecesHashes := infoDict["pieces"].(string)

	result, err := parsePiecesHashes(fileLength, pieceLength, pieceOffset, piecesHashes)

	if err != nil {
		return nil, fmt.Errorf("failed to parse pieces hashes: %w", err)
	}

	files := []file{{
		torrent:         &tr,
		Length:          fileLength,
		Name:            infoDict["name"].(string),
		Offset:          0,
		pieceEndIndex:   fileLength / pieceLength,
		pieceStartIndex: 0,
	}}

	return &torrentInfo{
		files:  files,
		pieces: result.pieces,
	}, nil
}

func parseInfoHash(xtParameter string) ([sha1.Size]byte, error) {
	var infoHash [sha1.Size]byte
	expectedHexEncodedLength := 40
	expectedBase32EncodedLength := 32
	infoHashURNPrefix := "urn:bith:"

	if !strings.HasPrefix(xtParameter, infoHashURNPrefix) {
		return infoHash, fmt.Errorf("info hash parameter contains an invalid prefix. expected '%s' got '%s'", infoHashURNPrefix, xtParameter)
	}

	encodedInfoHash := xtParameter[len(infoHashURNPrefix):]
	encodedInfoHashLength := len(encodedInfoHash)

	switch encodedInfoHashLength {
	case expectedHexEncodedLength:
		{
			decodedInfoHash, err := hex.DecodeString(encodedInfoHash)
			if err != nil {
				return infoHash, fmt.Errorf("failed to decode hex encoded info hash: %w", err)
			}
			copy(infoHash[:], decodedInfoHash)
		}
	case expectedBase32EncodedLength:
		{
			decodedInfoHash, err := base32.StdEncoding.DecodeString(encodedInfoHash)
			if err != nil {
				return infoHash, fmt.Errorf("failed to decode base32 encoded info hash: %w", err)
			}
			copy(infoHash[:], decodedInfoHash)
		}
	default:
		{
			return infoHash, fmt.Errorf("info hash must be %d or %d characters long, but received value is %d characters long", expectedHexEncodedLength, expectedBase32EncodedLength, encodedInfoHashLength)
		}
	}

	return infoHash, nil
}

func parseMagnetURL(magnetURL *url.URL) (Torrent, error) {
	var torrent Torrent

	if magnetURL.Scheme != "magnet" {
		return torrent, fmt.Errorf("URL scheme is invalid. expected \"magnet\" got \"%s\"", magnetURL.Scheme)
	}

	params, err := url.ParseQuery(magnetURL.RawQuery)

	if err != nil {
		return torrent, err
	}

	if infoHashParam, ok := params["xt"]; !ok || len(infoHashParam) != 1 {
		return torrent, fmt.Errorf("magnet URL must include an 'xt' (info hash) parameter")
	}

	if trackerParam, ok := params["tr"]; !ok || len(trackerParam) == 0 {
		return torrent, fmt.Errorf("magnet URL must include a 'tr' (list of trackers) parameter")
	}

	infoHash, err := parseInfoHash(params["xt"][0])

	if err != nil {
		return torrent, err
	}

	trackers := utils.NewSet()

	for _, tr := range params["tr"] {
		trackers.Add(tr)
	}

	ctx, cancelFunc := context.WithCancel(context.Background())

	torrent.ctx = ctx
	torrent.cancelFunc = cancelFunc

	torrent.infoHash = infoHash
	torrent.incomingPeersCh = make(chan []Peer, 1)
	torrent.maxPeerConnections = 10
	torrent.peerConnections = map[string]PeerConnection{}
	torrent.peers = make(map[string]Peer)
	torrent.failingPeers = make(map[string]Peer)
	torrent.statusCh = make(chan torrentStatus, 1)
	torrent.trackers = *trackers

	return torrent, nil
}

func parseTorrentFile(fileContent []byte) (Torrent, error) {
	var torrent Torrent

	decodedValue, _, err := bencode.DecodeValue(fileContent)

	if err != nil {
		return torrent, fmt.Errorf("failed to decode metainfo file: %w", err)
	}

	metainfo, ok := decodedValue.(map[string]any)

	if !ok {
		return torrent, fmt.Errorf("expected metainfo to be a bencoded dictionary, but received '%T'", metainfo)
	}

	for key, value := range map[string]any{"announce": "string", "info": make(map[string]any)} {
		if _, exists := metainfo[key]; !exists {
			return torrent, fmt.Errorf("metainfo dictionary is missing required property '%s'", key)
		}

		expectedType := reflect.TypeOf(value)
		receivedType := reflect.TypeOf(metainfo[key])

		if receivedType != expectedType {
			return torrent, fmt.Errorf("expected the '%s' property to be of type '%v', but received '%v'", key, expectedType, receivedType)
		}
	}

	var announceListErr error
	trackers := utils.NewSet()

	if announceList, ok := metainfo["announce-list"]; ok {
		trackers, announceListErr = parseAnnounceList(announceList)
	} else {
		trackers.Add(metainfo["announce"].(string))
	}

	if announceListErr != nil {
		return torrent, fmt.Errorf("failed to parse announce list: %w", announceListErr)
	}

	torrentInfo, err := parseInfoDict(metainfo["info"].(map[string]any), torrent)

	if err != nil {
		return torrent, fmt.Errorf("failed to parse metainfo 'info' dictionary %w", err)
	}

	bencodedValue, err := bencode.EncodeValue(metainfo["info"])

	if err != nil {
		return torrent, fmt.Errorf("failed to encode metainfo 'info' dictionary")
	}

	ctx, cancelFunc := context.WithCancel(context.Background())

	torrent.ctx = ctx
	torrent.cancelFunc = cancelFunc

	torrent.info = torrentInfo
	torrent.infoHash = sha1.Sum([]byte(bencodedValue))

	torrent.incomingPeersCh = make(chan []Peer, 1)
	torrent.maxPeerConnections = 10
	torrent.peerConnections = map[string]PeerConnection{}
	torrent.peers = make(map[string]Peer)
	torrent.failingPeers = make(map[string]Peer)
	torrent.statusCh = make(chan torrentStatus, 1)
	torrent.trackers = *trackers

	return torrent, nil
}

// Blacklists peers that have experienced multiple piece hash verifications.
func (tr *Torrent) handleBannedPeers() {
	for {
		select {
		case <-tr.ctx.Done():
			{
				return
			}
		case bannedPeerAddress := <-tr.bannedPeersCh:
			{
				tr.bannedPeers.Add(bannedPeerAddress)
			}
		}
	}
}

func (tr *Torrent) handleIncomingPeers() {
	for {
		// todo: handle failed peers
		select {
		case peers := <-tr.incomingPeersCh:
			{
				for _, peer := range peers {
					if len(tr.peerConnections) >= tr.maxPeerConnections {
						tr.peers[peer.String()] = peer
						break
					}

					if _, ok := tr.peerConnections[peer.String()]; ok {
						fmt.Println("peer already exists in connection pool")
						break
					}

					peerConnection := NewPeerConnection(PeerConnectionConfig{Peer: peer})

					if err := peerConnection.InitConnection(); err != nil {
						fmt.Printf("failed to connect to peer: %s: %v\n", peer, err)
						tr.failingPeers[peer.String()] = peer
						break
					}

					fmt.Printf("connected to peer: %s\n", peer)
					tr.peerConnections[peer.String()] = *peerConnection
				}
			}

		case <-tr.ctx.Done():
			{
				return
			}
		}
	}
}

func (tr *Torrent) handleStatusUpdate() {
	for {
		select {
		case <-tr.ctx.Done():
			{
				return
			}

		case status := <-tr.statusCh:
			{
				tr.status = status
			}
		}
	}
}

func (tr *Torrent) startAnnouncer() {
	// todo: make this function run in a interval
	// todo: handle failed trackers
	const announceInterval = 3 * time.Second
	ticker := time.NewTicker(announceInterval)

	defer ticker.Stop()

	for {
		select {
		case <-tr.ctx.Done():
			{
				// todo: notify trackers that we're stopping?
				return
			}

		case <-ticker.C:
			{
				var wg sync.WaitGroup

				maxConcurrency := 5
				sem := utils.NewSemaphore(maxConcurrency)

				for trackerUrl := range tr.trackers.Entries() {
					wg.Add(1)
					sem.Acquire()

					go func() {
						defer sem.Release()
						defer wg.Done()

						peers, err := tr.sendAnnounceRequest(trackerUrl)

						if err != nil {
							fmt.Println(err.Error())
							return
						}

						tr.incomingPeersCh <- peers
					}()
				}

				wg.Wait()
			}
		}
	}
}

// func (t *Torrent) DownloadMetadata() error {
// 	if t.Info.Pieces != nil {
// 		return nil
// 	}

// 	hasDownloadedAllPieces := false
// 	index := 0
// 	metadataBuffer := []byte{}
// 	peers, err := t.sendAnnounceRequest()

// 	if err != nil {
// 		return nil
// 	}

// 	for i, numOfPeers := 0, len(peers); i < numOfPeers && !hasDownloadedAllPieces; i++ {
// 		peerConnection := NewPeerConnection(PeerConnectionConfig{Peer: peers[i]})

// 		if err := peerConnection.InitConnection(); err != nil {
// 			peerConnection.Close()
// 			continue
// 		}

// 		for {
// 			metadataPiece, err := downloadMetadataPiece(*peerConnection, index)

// 			if err != nil {
// 				break
// 			}

// 			metadataBuffer = append(metadataBuffer, metadataPiece...)
// 			index += 1

// 			//  If it is not the last piece of the metadata, it MUST be 16kiB (BlockSize).
// 			if len(metadataPiece) != BlockSize {
// 				hasDownloadedAllPieces = true
// 				break
// 			}
// 		}
// 	}

// 	if !hasDownloadedAllPieces {
// 		return fmt.Errorf("failed to download torrent metadata from %d peers", len(peers))
// 	}

// 	metadataHash := sha1.Sum(metadataBuffer)

// 	if !bytes.Equal(t.InfoHash[:], metadataHash[:]) {
// 		return fmt.Errorf("downloaded metadata hash is does not match torrent info hash")
// 	}

// 	decoded, _, err := bencode.DecodeValue(metadataBuffer)

// 	if err != nil {
// 		return fmt.Errorf("failed to decode downloaded metadata: %w", err)
// 	}

// 	infoDict, ok := decoded.(map[string]any)

// 	if !ok {
// 		return fmt.Errorf("expected the decoded payload to be a dict, but got %T", infoDict)
// 	}

// 	var torrentInfo TorrentInfo

// 	if err := mapstructure.Decode(infoDict, &torrentInfo); err != nil {
// 		return err
// 	}

// 	pieces, err := parseTorrentPieces(infoDict)

// 	if err != nil {
// 		return err
// 	}

// 	torrentInfo.Pieces = pieces
// 	t.Info = torrentInfo

// 	return nil
// }

func (t *Torrent) Start() {
	go t.startAnnouncer()
	go t.handleIncomingPeers()
	go t.handleStatusUpdate()
	go t.handleBannedPeers()

	// todo: move this signal handler to a higher-level (session)
	signalsCh := make(chan os.Signal, 1)
	signal.Notify(signalsCh, syscall.SIGINT, syscall.SIGTERM)

	<-signalsCh
	fmt.Println("shutting down...")
	t.Stop()
	fmt.Println("successfully closed all peer connections.")
}

// Cancels all active goroutines and gracefully shuts down all active peer connections
func (t *Torrent) Stop() {
	t.cancelFunc()

	for _, connection := range t.peerConnections {
		connection.Close()
	}

	// todo: close channels?
}

func NewTorrent(src string) (Torrent, error) {
	var torrent Torrent
	var err error

	if utils.FileExists(src) {
		fileContent, err := os.ReadFile(src)

		if err != nil {
			return torrent, fmt.Errorf("failed to read torrent file '%s' :%w", src, err)
		}

		torrent, err = parseTorrentFile(fileContent)
		return torrent, err
	}

	parsedUrl, err := url.Parse(src)

	if err != nil {
		return torrent, fmt.Errorf("torrent src must be a path to a \".torrent\" file or a URL")
	}

	switch parsedUrl.Scheme {
	case "http", "https":
		{
			resp, err := http.DefaultClient.Get(src)

			if err != nil {
				return torrent, fmt.Errorf("HTTP request failed: %w", err)
			}

			defer resp.Body.Close()

			statusOK := resp.StatusCode >= 200 && resp.StatusCode < 300

			if !statusOK {
				return torrent, fmt.Errorf("received NON-OK HTTP status code \"%d\"", resp.StatusCode)
			}

			content, err := io.ReadAll(resp.Body)

			if err != nil {
				return torrent, fmt.Errorf("failed to read HTTP response body: %w", err)
			}

			torrent, err = parseTorrentFile(content)

			return torrent, err
		}

	case "magnet":
		{
			torrent, err = parseMagnetURL(parsedUrl)
			return torrent, err
		}

	default:
		{
			return torrent, fmt.Errorf("torrent URL scheme must be one of \"http\", \"https\" or \"magnet\"")
		}
	}
}
