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
	metadataDownloaderCancelFunc context.CancelFunc

	bannedPeers        utils.Set
	bannedPeersCh      chan string
	failingPeers       map[string]Peer
	incomingPeersCh    chan []Peer
	maxPeerConnections int
	metadataPeersCh    chan PeerConnection
	peerConnectionPool *peerConnectionPool
	peers              map[string]Peer

	downloadedPieces chan DownloadedPiece
	failedPiecesCh   chan Piece
	queuedPiecesCh   chan Piece

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

/*
Enqueues pieces of the torrent for processing. It ensures that
all pieces from the torrent's metadata are added to the queuedPiecesCh channel.
If a piece fails during processing, it is re-enqueued from the failedPiecesCh
channel. The function requires that the torrent metadata (tr.info) is
available; otherwise, it will panic.
*/
func (tr *Torrent) enquePieces() {
	if tr.metadataDownloaderCtx == nil {
		panic("metadataDownloaderCtx is not initialized")
	}

	select {
	case <-tr.metadataDownloaderCtx.Done():
		break
	case <-tr.ctx.Done():
		return
	}

	if tr.info == nil {
		panic("torrent metadata is not available; cannot enqueue pieces")
	}

	for _, piece := range tr.info.pieces {
		select {
		case <-tr.ctx.Done():
			return
		case failedPiece := <-tr.failedPiecesCh:
			tr.queuedPiecesCh <- failedPiece
		default:
			tr.queuedPiecesCh <- piece
		}
	}

	// Continuously listen for failed pieces and re-enqueue them until the context is canceled.
	for {
		select {
		case <-tr.ctx.Done():
			return
		case failedPiece := <-tr.failedPiecesCh:
			tr.queuedPiecesCh <- failedPiece
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
					if tr.peerConnectionPool.size() >= tr.maxPeerConnections {
						// todo: place a limit on the number of idle peers
						tr.peers[peer.String()] = peer
						break
					}

					if _, ok := tr.peerConnectionPool.connections[peer.String()]; ok {
						fmt.Println("peer already exists in connection pool")
						continue
					}

					peerConnection := NewPeerConnection(peerConnectionConfig{Peer: peer})

					if err := peerConnection.InitConnection(); err != nil {
						fmt.Printf("failed to connect to peer: %s: %v\n", peer, err)
						tr.failingPeers[peer.String()] = peer
						continue
					}

					fmt.Printf("connected to peer: %s\n", peer)
					tr.peerConnectionPool.addConnection(*peerConnection)

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

// startAnnouncer starts the announcer routine for the Torrent instance.
// This function periodically sends announce requests to the trackers associated
// with the torrent to retrieve peer information. It runs in an interval defined
// by `announceInterval` and handles concurrency for sending requests to multiple
// trackers.
//
// The function uses a ticker to trigger periodic announce requests and listens
// for a cancellation signal from the context (`tr.ctx.Done()`) to gracefully stop
// the announcer. When stopping, it optionally allows for notifying trackers
// about the stop event (currently marked as a TODO).
//
// Key Features:
// - Runs announce requests at a fixed interval.
// - Handles failed tracker requests gracefully by logging errors.
// - Limits the number of concurrent requests to trackers using a semaphore.
// - Sends retrieved peer information to the `incomingPeersCh` channel.
//
// Note: This function assumes that `tr.trackers.Entries()` provides a list of
// tracker URLs and that `tr.sendAnnounceRequest(trackerUrl)` handles the actual
// announce request logic, returning a list of peers or an error.
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
				tr.metadataDownloaderCancelFunc()
			}
		}
	}
}

// func (tr *Torrent) startPieceDownloader() {
// 	maxConcurrency := 10
// 	sem := utils.NewSemaphore(maxConcurrency)

// 	var wg sync.WaitGroup

// 	for {
// 		select {
// 		case <-tr.ctx.Done():
// 			{
// 				return
// 			}

// 		case piece := <-tr.queuedPiecesCh:
// 			{
				
// 				/*
// 								get piece from the queue and attempt. Get a peer connection from queue and attempt to dowload from

// 								get a peer connection from the peer connection channel, this must content with the cancel of the max torrent function

// 								select {
// 									case pc := <- tr.connectionPool.connections: {}
// 									case <- tr.ctx.Done(): {
// 										return
// 									}
// 								}

// 				*/
// 			}
// 		}
// 	}

// 	// todo: make 'peerConnections' a channel ?
// 	for _, peerconn := range tr.peerConnections {
// 		sem.Acquire()
// 		wg.Add(1)

// 		go func(pc PeerConnection) {
// 			defer sem.Release()
// 			defer wg.Done()

// 			for {
// 				select {
// 				case <-tr.ctx.Done():
// 					return
// 				case piece := <-tr.queuedPiecesCh:
// 					{
// 						// todo: handle errors caused by peer not having the given piece
// 						downloadedPiece, err := pc.DownloadPiece(piece)

// 						if err != nil && pc.failedAttempts >= peerConnectionMaxFailedAttempts {
// 							// todo: send to 'failedPeersCh"
// 							return
// 						}

// 						if err != nil {
// 							pc.failedAttempts += 1
// 							tr.failedPiecesCh <- piece
// 							continue
// 						}

// 						if err := downloadedPiece.CheckHashIntegrity(); err != nil {
// 							// todo: do we simple increment the fail count for peer that send piece that fail the hash check?
// 							pc.failedAttempts += 1
// 							tr.failedPiecesCh <- piece
// 							continue
// 						}

// 						tr.downloadedPieces <- *downloadedPiece
// 					}
// 					// todo: listen for download completetion
// 				}
// 			}

// 		}(peerconn)

// 		wg.Wait()
// 	}

// }

func (t *Torrent) Start() {
	go t.startAnnouncer()
	go t.handleIncomingPeers()
	go t.handleStatusUpdate()
	go t.handleBannedPeers()
	go t.startMetadataDownloader()
	go t.enquePieces()
	// go t.startPieceDownloader()

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
	t.peerConnectionPool.closeConnections()
	// todo: close channels?
}

/*
	piece downloader waits on the main "ctx" and the "metadataDownloaderCtx" contexts.
	When the main context is cancelled, it stops downloading pieces and closes all peer connections.
	When the metadata downloader context is cancelled, it starts downloading pieces.


	Scenario 1:
		goroutine a:
			loops through list of pieces for the torrent and adds them to a channel (maximum 10 at a time).

		goroutine b:
			this is the downloader. It loops through the download channel and attempts to download pieces
			if successful, it places the downloaded piece on another channel which is used by the piece write
			if it fails, it needs to place the piece request back on the source channel so it can be downloaded again.

		goroutine c:
			receives piece response and attempts to write the downloaded data to storage.
			if it fails, and the error can be deemed as a transient error, retry the write with backoff.
			if it fails after a maximum number of tries or it's a critical error stop the torrent.

			if it succeeds update the total number of pieces downloaded counter
			if we've downloaded all pieces cancel the "piecedownload" ctx forcing the first two goroutines to exit.

		questions:
			how do we sync piece request movement between goroutine a and b.

			if gorutine a is blocked because it has downloaded add a max number of requests
			but goroutines b needs to put a failed request back on the queue what happens?

*/
