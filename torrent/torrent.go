package torrent

import (
	"bytes"
	"context"
	"crypto/sha1"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/MlkMahmud/hail/bencode"
	"github.com/MlkMahmud/hail/utils"
)

type file struct {
	length          int
	name            string
	offset          int
	pieceEndIndex   int
	pieceStartIndex int
}

type torrentInfo struct {
	files  []file
	length int
	name   string
	pieces []piece
}

type torrentStatus int

const (
	initializing torrentStatus = iota
	connecting
	downloadingMetadata
	downloading
	finished
	flushing
	completed
	stopped
)

type NewTorrentOpts struct {
	PeerId    [20]byte
	OutputDir string
	Src       string
}

// todo: use mutexes to wrap properties accessed by multiple goroutines
type Torrent struct {
	info *torrentInfo

	infoHash [sha1.Size]byte

	bannedPeersCh               chan string
	connectedCh                 chan struct{}
	downloadedPieceCh           chan downloadedPiece
	failedPiecesCh              chan piece
	incomingPeersCh             chan []peer
	metadataDownloadCompletedCh chan struct{}
	piecesDownloadCompleteCh    chan struct{}
	queuedPiecesCh              chan piece
	statusCh                    chan torrentStatus
	stoppedCh                   chan struct{}

	bannedPeers     utils.Set
	failingTrackers utils.Set
	trackers        utils.Set

	failingPeers map[string]peer
	peers        map[string]peer

	downloaded         int
	downloadedPieces   int
	maxPeerConnections int

	peerId                      [20]byte
	metadataDownloadCompleted   bool
	metadataDownloadCompletedMu sync.Mutex
	metadataPeersCh             chan peerConnection
	peerConnectionPool          *peerConnectionPool
	outputDir                   string
	status                      torrentStatus
}

func NewTorrent(opts NewTorrentOpts) (*Torrent, error) {
	if utils.FileExists(opts.Src) {
		fileContent, err := os.ReadFile(opts.Src)

		if err != nil {
			return nil, fmt.Errorf("failed to read torrent file '%s' :%w", opts.Src, err)
		}

		return newTorrentFromMetainfoFile(fileContent, opts)
	}

	parsedUrl, err := url.Parse(opts.Src)

	if err != nil {
		return nil, fmt.Errorf("torrent src must be a path to a \".torrent\" file or a URL")
	}

	switch parsedUrl.Scheme {
	case "http", "https":
		{
			resp, err := http.DefaultClient.Get(opts.Src)

			if err != nil {
				return nil, fmt.Errorf("failed to fetch torrent file from URL '%s': %w", opts.Src, err)
			}

			defer resp.Body.Close()

			statusOK := resp.StatusCode >= 200 && resp.StatusCode < 300

			if !statusOK {
				return nil, fmt.Errorf("received NON-OK HTTP status code \"%d\" while attempting to fetch torrent file from URL '%s'. Please check the URL or try again later", resp.StatusCode, opts.Src)
			}

			content, err := io.ReadAll(resp.Body)

			if err != nil {
				return nil, fmt.Errorf("failed to read HTTP response body from URL '%s': %w", opts.Src, err)
			}

			return newTorrentFromMetainfoFile(content, opts)
		}

	case "magnet":
		{
			return newTorrentFromMagnetURL(parsedUrl, opts)
		}

	default:
		{
			return nil, fmt.Errorf("unsupported torrent URL scheme \"%s\". The URL scheme must be one of \"http\", \"https\", or \"magnet\". Please check the provided URL: '%s'", parsedUrl.Scheme, opts.Src)
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
func (tr *Torrent) enqueuePieces(ctx context.Context) {
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
					if _, ok := tr.peerConnectionPool.connections[peer.String()]; ok {
						continue
					}

					pc := newPeerConnection(peerConnectionOpts{
						infoHash:   tr.infoHash,
						peerId:     tr.peerId,
						remotePeer: peer,
					})

					// todo: place a limit on the number of idle peers
					tr.peers[peer.String()] = peer

					if tr.peerConnectionPool.size() < tr.maxPeerConnections {
						tr.peerConnectionPool.addConnection(*pc)
					}

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
				switch status {
				case completed:
					log.Println("torrent download has completed.")
				case connecting:
					log.Println("connecting to peers...")
				case downloading:
					log.Println("downloading torrent files...")
				case downloadingMetadata:
					log.Println("downloading metadata...")
				case finished:
					log.Println("finished downloading all pieces")
				case flushing:
					log.Println("writing downloaded pieces to disk...")
				case stopped:
					log.Println("stopping torrent...")
				}
			}
		}

	}
}

// Marks the metadata download process as completed for the Torrent instance.
// It ensures thread-safe access to the metadata download state using a mutex lock.
// If the metadata download has not been marked as completed yet, it closes the metadata download channel
// and updates the state to indicate completion.
func (tr *Torrent) markMetadataDownloadCompleted() {
	tr.metadataDownloadCompletedMu.Lock()
	defer tr.metadataDownloadCompletedMu.Unlock()

	if !tr.metadataDownloadCompleted {
		close(tr.metadataDownloadCompletedCh)
		tr.metadataDownloadCompleted = true
	}
}

func (tr *Torrent) printProgress() {
	var progress float64

	if tr.info.length > 0 {
		progress = float64(tr.downloaded) / float64(tr.info.length) * 100
	}
	log.Printf("(%.2f%%) downloaded %d piece(s) from %d peers\n", progress, tr.downloadedPieces, tr.peerConnectionPool.size())
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
	tr.updateStatus(connecting)

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

				for trackerUrl := range tr.trackers.Entries() {
					wg.Add(1)
					sem.Acquire()

					go func() {
						defer sem.Release()
						defer wg.Done()

						peers, err := tr.sendAnnounceRequest(trackerUrl)

						if err != nil {
							log.Printf("announce request failed: %v", err)
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
		tr.markMetadataDownloadCompleted()
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
					log.Println(err)
					break
				}

				defer pc.close()

				if !pc.supportsExtension(metadataExt) {
					log.Printf("peer connection \"%s\" does not support the metadata extension\n", pc.remotePeerAddress)
					break
				}

				tr.updateStatus(downloadingMetadata)

				metadata, err := pc.downloadMetadata()

				if err != nil {
					log.Println(err)
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

				info, err := parseInfoDict(metadataDict)

				if err != nil {
					break
				}

				tr.info = info
				tr.markMetadataDownloadCompleted()
				return
			}
		}
	}
}

func (tr *Torrent) startPieceDownloader(ctx context.Context) {
	maxConcurrency := 5
	sem := utils.NewSemaphore(maxConcurrency)
	tr.updateStatus(downloading)

	for {
		select {
		case <-ctx.Done():
			return

		default:
			{
				sem.Acquire()
				conn, err := tr.peerConnectionPool.getIdleConnection(ctx)

				select {
				case <-ctx.Done():
					sem.Release()
					return
				default:
					if err != nil {
						log.Println(err)
						sem.Release()
						break
					}
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
								if err := pc.initConnection(peerConnectionInitConfig{bitfieldSize: len(tr.info.pieces)}); err != nil {
									log.Println(err)
									tr.peerConnectionPool.removeConnection(pc.remotePeerAddress)
									return
								}

								downloadedPiece, err := pc.downloadPiece(piece)

								if err != nil && pc.failedAttempts >= peerConnectionMaxFailedAttempts {
									// todo: send to 'failedPeersCh"
									tr.peerConnectionPool.removeConnection(pc.remotePeerAddress)
									return
								}

								if err != nil {
									log.Println(err)
									pc.failedAttempts += 1
									tr.failedPiecesCh <- piece
									continue
								}

								if err := downloadedPiece.validateIntegrity(); err != nil {
									// todo: do we simple increment the fail count for peer that send piece that fail the hash check?
									log.Println(err)
									pc.failedAttempts += 1
									tr.failedPiecesCh <- piece
									continue
								}

								tr.downloadedPieceCh <- *downloadedPiece
							}
						}
					}
				}(conn, tr)
			}
		}
	}
}

func (tr *Torrent) startPieceWriter(ctx context.Context, piecesDir string) {
	for {
		select {
		case <-ctx.Done():
			return

		case downloadedPiece := <-tr.downloadedPieceCh:
			{
				if err := downloadedPiece.writeToDisk(piecesDir); err != nil {
					log.Printf("failed to write piece #%d to disk\n", downloadedPiece.piece.index)
					tr.failedPiecesCh <- downloadedPiece.piece
					continue
				}

				tr.downloaded += downloadedPiece.piece.length
				tr.downloadedPieces += 1
				tr.printProgress()

				if tr.downloadedPieces >= len(tr.info.pieces) {
					tr.updateStatus(finished)
					close(tr.piecesDownloadCompleteCh)
					return
				}
			}
		}
	}
}

func (tr *Torrent) updateStatus(status torrentStatus) {
	// todo: wrap status read op with mutex
	if tr.status != status {
		tr.statusCh <- status
	}
}

func (tr *Torrent) writeFilesToDisk(ctx context.Context, piecesDir string) error {
	tr.updateStatus(flushing)

	for _, file := range tr.info.files {
		select {
		case <-ctx.Done():
			return nil
		default:
			err := os.MkdirAll(tr.outputDir, 0755)

			if err != nil {
				return err
			}

			dest, err := os.Create(filepath.Join(tr.outputDir, file.name))

			if err != nil {
				return fmt.Errorf("failed to create file '%s': %w\n", file.name, err)
			}

			for index := file.pieceStartIndex; index <= file.pieceEndIndex; index++ {
				select {
				case <-ctx.Done():
					dest.Close()
					return nil
				default:
					isInitialPiece := file.pieceStartIndex == index
					path := filepath.Join(piecesDir, fmt.Sprintf("%020d.piece", index))
					offset := 0

					if isInitialPiece {
						offset = file.offset
					}

					fptr, err := os.Open(path)

					if err != nil {
						return fmt.Errorf("failed to open file '%s': %w\n", path, err)
					}

					defer fptr.Close()

					if _, err := fptr.Seek(int64(offset), io.SeekStart); err != nil {
						return fmt.Errorf("failed to seek to offset %d in file '%s': %w\n", offset, path, err)
					}

					content, err := io.ReadAll(fptr)

					if err != nil {
						return fmt.Errorf("failed to read data from file '%s': %w\n", path, err)
					}

					if _, err := dest.Write(content); err != nil {
						return fmt.Errorf("failed to write data from piece '%d' to file '%s': %w\n", index, dest.Name(), err)
					}
				}
			}

			dest.Close()
		}
	}

	return nil
}

func (tr *Torrent) ID() string {
	return fmt.Sprintf("%x", tr.infoHash)
}

// Initializes and manages the lifecycle of the torrent download process.
//
// It sets up various goroutines to handle peer connections, metadata downloading,
// piece downloading, and writing. The function listens for system signals to
// gracefully shut down all active processes and connections. It also ensures
// that the torrent's metadata is downloaded before starting the piece download
// process. The function uses context objects to manage the cancellation of
// goroutines and ensures proper cleanup of resources upon shutdown.
func (t *Torrent) Start() {
	ctx, cancelCtx := context.WithCancel(context.Background())
	downloaderCtx, cancelDownloader := context.WithCancel(ctx)

	t.downloaded = 0
	t.downloadedPieces = 0
	t.stoppedCh = make(chan struct{}, 1)

	go t.handleStatusUpdate(ctx)

	go t.startAnnouncer(ctx)
	go t.handleIncomingPeers(ctx)
	go t.handleBannedPeers(ctx)
	go t.startMetadataDownloader(ctx)

	shutdownFn := func(status torrentStatus) {
		t.updateStatus(status)
		cancelDownloader()
		cancelCtx()
		t.peerConnectionPool.closeConnections()
	}

	var tempDir string

	select {
	case <-t.stoppedCh:
		shutdownFn(stopped)
		return

	case <-t.metadataDownloadCompletedCh:
		dir, err := os.MkdirTemp("/tmp", t.info.name)
		tempDir = dir

		if err != nil {
			log.Printf("failed to create pieces directory for torrent '%s': %v\n", t.info.name, err)
			shutdownFn(stopped)
			return
		}

		defer os.RemoveAll(tempDir)

		go t.enqueuePieces(downloaderCtx)
		go t.startPieceDownloader(downloaderCtx)
		go t.startPieceWriter(downloaderCtx, tempDir)
	}

	select {
	case <-t.stoppedCh:
		shutdownFn(stopped)

	case <-t.piecesDownloadCompleteCh:
		err := t.writeFilesToDisk(ctx, tempDir)
		status := completed

		if err != nil {
			log.Printf("failed to write files to disk: %v\n", err)
			status = stopped
		}

		shutdownFn(status)
	}
}

func (t *Torrent) Stop() {
	if t.stoppedCh == nil {
		return
	}

	channelIsClosed := false

	select {
	case _, ok := <-t.stoppedCh:
		if !ok {
			channelIsClosed = true
		}
	default:
	}

	if !channelIsClosed {
		close(t.stoppedCh)
	}
}
