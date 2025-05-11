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

	metadataDownloadCompletedCh chan struct{}
	piecesDownloadCompleteCh    chan struct{}

	bannedPeers        utils.Set
	bannedPeersCh      chan string
	failingPeers       map[string]Peer
	incomingPeersCh    chan []Peer
	maxPeerConnections int
	metadataPeersCh    chan peerConnection
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
func (tr *Torrent) enquePieces(ctx context.Context) {
	if tr.info == nil {
		panic("torrent metadata is not available; cannot enqueue pieces")
	}

	for _, piece := range tr.info.pieces {
		select {
		case <-ctx.Done():
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
		case <-ctx.Done():
			return
		case failedPiece := <-tr.failedPiecesCh:
			tr.queuedPiecesCh <- failedPiece
		}
	}
}

// Blacklists peers that have experienced multiple piece hash verifications.
func (tr *Torrent) handleBannedPeers(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
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

func (tr *Torrent) handleIncomingPeers(ctx context.Context) {
	for {
		// todo: handle failed peers
		select {
		case <-ctx.Done():
			return

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

					pc := newPeerConnection(peer)
					tr.peerConnectionPool.addConnection(*pc)

					select {
					case <-tr.metadataDownloadCompletedCh:
						continue
					case tr.metadataPeersCh <- *pc:
					}
				}
			}
		}
	}
}

func (tr *Torrent) handleStatusUpdate(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
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
func (tr *Torrent) startAnnouncer(ctx context.Context) {
	// todo: make this function run in a interval
	// todo: handle failed trackers
	announceInterval := 3 * time.Second
	ticker := time.NewTicker(announceInterval)

	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			{
				// todo: notify trackers that we're stopping?
				return
			}

		case <-ticker.C:
			{
				var wg sync.WaitGroup

				maxConcurrency := 5
				sem := utils.NewSemaphore(maxConcurrency)

				fmt.Println("discovering peers....")

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
				announceInterval = 5 * time.Minute
				ticker.Reset(announceInterval)
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
func (tr *Torrent) startMetadataDownloader(ctx context.Context) {
	if tr.info != nil {
		return
	}

	for {
		select {
		case <-ctx.Done():
			return

		case pc := <-tr.metadataPeersCh:
			{
				// todo: add debug logs
				if err := pc.initConnection(peerConnectionInitConfig{bitfieldSize: 0}); err != nil {
					fmt.Println(err)
					break
				}

				if !pc.supportsExtension(Metadata) {
					fmt.Printf("peer connection \"%s\" does not support the metadata extension\n", pc.peerAddress)
					pc.close()
					break
				}

				metadata, err := pc.downloadMetadata()

				if err != nil {
					fmt.Println(err)
					pc.close()
					break
				}

				if metadataHash := sha1.Sum(metadata); !bytes.Equal(tr.infoHash[:], metadataHash[:]) {
					// todo: blacklist peer?
					pc.close()
					break
				}

				decodedValue, _, err := bencode.DecodeValue(metadata)

				if err != nil {
					pc.close()
					break
				}

				metadataDict, ok := decodedValue.(map[string]any)

				if !ok {
					pc.close()
					break
				}

				info, err := parseInfoDict(metadataDict, *tr)

				if err != nil {
					pc.close()
					break
				}

				tr.info = info
				pc.close()
				tr.metadataDownloadCompletedCh <- struct{}{}
				close(tr.metadataDownloadCompletedCh)
				return
			}
		}
	}
}

func (tr *Torrent) startPieceDownloader(ctx context.Context) {
	maxConcurrency := 10
	sem := utils.NewSemaphore(maxConcurrency)

	for {
		select {
		case <-ctx.Done():
			return

		default:
			{
				fmt.Println("starting a piece downloader")

				sem.Acquire()
				conn, err := tr.peerConnectionPool.getIdleConnection(ctx)

				if err != nil {
					fmt.Println(err)
					sem.Release()
					break
				}

				go func(pc peerConnection, tr *Torrent) {
					defer sem.Release()

					for {
						select {
						case <-ctx.Done():
							{
								return
							}

						case piece := <-tr.queuedPiecesCh:
							{
								fmt.Println("downloading pieces")

								if err := pc.initConnection(peerConnectionInitConfig{bitfieldSize: len(tr.info.pieces)}); err != nil {
									fmt.Println(err)
									tr.peerConnectionPool.removeConnection(pc.peerAddress)
									return
								}

								downloadedPiece, err := pc.downloadPiece(piece)

								if err != nil && pc.failedAttempts >= peerConnectionMaxFailedAttempts {
									// todo: send to 'failedPeersCh"
									fmt.Printf("failed to downloaded piece: '%d'\n", piece.Index)
									fmt.Printf("removing peer connection: \"%s\"\n", pc.peerAddress)
									tr.peerConnectionPool.removeConnection(pc.peerAddress)
									return
								}

								if err != nil {
									fmt.Println(err)
									fmt.Printf("failed to downloaded piece: '%d'\n", piece.Index)
									pc.failedAttempts += 1
									tr.failedPiecesCh <- piece
									continue
								}

								if err := downloadedPiece.CheckHashIntegrity(); err != nil {
									// todo: do we simple increment the fail count for peer that send piece that fail the hash check?
									fmt.Println(err)
									fmt.Printf("failed to downloaded piece: '%d'\n", piece.Index)
									pc.failedAttempts += 1
									tr.failedPiecesCh <- piece
									continue
								}
								fmt.Printf("successfully downloaded piece %d\n", piece.Index)
								tr.downloadedPieces <- *downloadedPiece
								fmt.Printf("added piece %d to downloaded queue\n", piece.Index)
							}
						}
					}
				}(conn, tr)
			}
		}
	}
}

func (tr *Torrent) startPieceWriter(ctx context.Context) {
	numOfDownloadedPieces := 0

	for {
		select {
		case <-ctx.Done():
			return

		case downloadedPiece := <-tr.downloadedPieces:
			{
				fmt.Printf("starting piece writer for piece \"%d\"\n", downloadedPiece.Piece.Index)

				dest := fmt.Sprintf("piece-%d", downloadedPiece.Piece.Index)

				if err := os.WriteFile(dest, downloadedPiece.Data, 0666); err != nil {
					tr.failedPiecesCh <- downloadedPiece.Piece
				}

				numOfDownloadedPieces += 1

				if numOfDownloadedPieces >= len(tr.info.pieces) {
					tr.piecesDownloadCompleteCh <- struct{}{}
					close(tr.piecesDownloadCompleteCh)
					return
				}
			}
		}
	}
}

// Start initializes and manages the lifecycle of the torrent download process.
// It sets up various goroutines to handle peer connections, metadata downloading,
// piece downloading, and writing. The function listens for system signals to
// gracefully shut down all active processes and connections. It also ensures
// that the torrent's metadata is downloaded before starting the piece download
// process. The function uses context objects to manage the cancellation of
// goroutines and ensures proper cleanup of resources upon shutdown.
func (t *Torrent) Start() {
	ctx, cancelCtx := context.WithCancel(context.Background())
	downloaderCtx, cancelDownloader := context.WithCancel(ctx)

	go t.startAnnouncer(ctx)
	go t.handleIncomingPeers(ctx)
	go t.handleStatusUpdate(ctx)
	go t.handleBannedPeers(ctx)
	go t.startMetadataDownloader(ctx)

	// todo: move this signal handler to a higher-level (session)
	// todo: pass a context object from the session to the "start" and "stop" functions.
	signalsCh := make(chan os.Signal, 1)
	signal.Notify(signalsCh, syscall.SIGINT, syscall.SIGTERM)

	shutdownFn := func() {
		fmt.Println("shutting down...")
		cancelCtx()
		cancelDownloader()
		t.peerConnectionPool.closeConnections()
		fmt.Println("successfully closed all peer connections.")
		// Do not close signalsCh to avoid potential panic from sending to a closed channel
	}

	select {
	case <-signalsCh:
		shutdownFn()
		return
	case <-t.metadataDownloadCompletedCh:
		go t.enquePieces(downloaderCtx)
		go t.startPieceDownloader(downloaderCtx)
		go t.startPieceWriter(downloaderCtx)
	}

	select {
	case <-signalsCh:
		shutdownFn()
		return
	case <-t.piecesDownloadCompleteCh:
		cancelDownloader()
	}

	<-signalsCh
	shutdownFn()
}

// Cancels all active goroutines and gracefully shuts down all active peer connections
// func (t *Torrent) Stop() {

// 	t.peerConnectionPool.closeConnections()
// 	// todo: close channels?
// }
