package torrent

import (
	"bytes"
	"context"
	"crypto/sha1"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
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

type Torrent struct {
	info     *torrentInfo
	infoHash [sha1.Size]byte

	ctx        context.Context
	cancelFunc context.CancelFunc

	metadataDownloaderCtx      context.Context
	metadataDownloadCancelFunc context.CancelFunc

	bannedPeers        utils.Set
	bannedPeersCh      chan string
	failingPeers       map[string]Peer
	incomingPeersCh    chan []Peer
	maxPeerConnections int
	metadataPeersCh    chan PeerConnection
	peerConnections    map[string]PeerConnection
	peers              map[string]Peer

	failingTrackers utils.Set
	trackers        utils.Set

	status   torrentStatus
	statusCh chan torrentStatus
}

func NewTorrent(src string) (Torrent, error) {
	var torrent Torrent
	var err error

	if utils.FileExists(src) {
		fileContent, err := os.ReadFile(src)

		if err != nil {
			return torrent, fmt.Errorf("failed to read torrent file '%s' :%w", src, err)
		}

		torrent, err = parseMetaInfo(fileContent)
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

			torrent, err = parseMetaInfo(content)

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
						continue
					}

					peerConnection := NewPeerConnection(PeerConnectionConfig{Peer: peer})

					if err := peerConnection.InitConnection(); err != nil {
						fmt.Printf("failed to connect to peer: %s: %v\n", peer, err)
						tr.failingPeers[peer.String()] = peer
						continue
					}

					fmt.Printf("connected to peer: %s\n", peer)
					tr.peerConnections[peer.String()] = *peerConnection

					if !peerConnection.supportsExtension(Metadata) || tr.metadataDownloaderCtx == nil {
						continue
					}

					select {
					case <-tr.metadataDownloaderCtx.Done():
						continue
					case tr.metadataPeersCh <- *peerConnection:
					}
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

/*
Downloads the torrent's metadata (info) if it hasn't been downloaded yet.

This function listens for peer connections on the `metadataPeersCh` channel and attempts to download
the torrent's metadata from each peer connection. If the metadata is successfully downloaded and verified
against the torrent's info hash, the metadata is decoded and stored in the torrent's `info` field.

On success, the function cancels the metadata downloader context, signalling the goroutine responsible
for sending peer connections to the `metadataPeersCh` channel to stop sending further connections.

For example:

 1. A peer connection is received via the `metadataPeersCh` channel.

 2. The function requests the metadata from the peer using the `downloadMetadata` method.

 3. If the metadata matches the torrent's info hash, it is decoded and stored.

 4. The metadata downloader context is canceled to stop further metadata requests and signal other goroutines.

If the metadata download fails or the hash does not match, the function continues to process other peer connections.
*/
func (tr *Torrent) startMetadataDownloader() {
	if tr.info != nil {
		return
	}

	ctx, cancelFunc := context.WithCancel(context.Background())
	tr.metadataDownloaderCtx = ctx
	tr.metadataDownloadCancelFunc = cancelFunc

	for {
		select {
		case <-tr.ctx.Done():
			{
				return
			}
		case peerConnection := <-tr.metadataPeersCh:
			{
				// request peer connection. If successful cancel the metadata downloader context and signal to downloader that i can start.
				// todo: add debug logs
				metadata, err := peerConnection.downloadMetadata()

				if err != nil {
					break
				}

				if metadataHash := sha1.Sum(metadata); !bytes.Equal(tr.infoHash[:], metadataHash[:]) {
					// todo: blacklist peer?
					break
				}

				decodedValue, _, err := bencode.DecodeValue(metadata)

				if err != nil {
					break
				}

				metadataDict, ok := decodedValue.(map[string]any)

				if !ok {
					break
				}

				info, err := parseInfoDict(metadataDict, *tr)

				if err != nil {
					break
				}

				tr.info = info
				tr.metadataDownloadCancelFunc()
			}
		}
	}
}

func (t *Torrent) Start() {
	go t.startAnnouncer()
	go t.handleIncomingPeers()
	go t.handleStatusUpdate()
	go t.handleBannedPeers()
	go t.startMetadataDownloader()

	// todo: move this signal handler to a higher-level (session)
	signalsCh := make(chan os.Signal, 1)
	signal.Notify(signalsCh, syscall.SIGINT, syscall.SIGTERM)

	<-signalsCh
	fmt.Println("shutting down...")
	t.Stop()
	fmt.Println("successfully closed all peer connections.")
	close(signalsCh)
}

// Cancels all active goroutines and gracefully shuts down all active peer connections
func (t *Torrent) Stop() {
	t.cancelFunc()

	for _, connection := range t.peerConnections {
		connection.Close()
	}

	// todo: close channels?
}
