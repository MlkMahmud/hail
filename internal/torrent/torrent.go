package torrent

import (
	"bytes"
	"context"
	"crypto/sha1"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/MlkMahmud/hail/internal/bencode"
	"github.com/MlkMahmud/hail/internal/utils"
)

type torrentInfo struct {
	files       []file
	length      int
	pieceLength int
	pieces      []piece
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
	errored
)

type NewTorrentOpts struct {
	Logger    *slog.Logger
	PeerId    [20]byte
	OutputDir string
	Src       string
}

type Torrent struct {
	info *torrentInfo

	infoHash [sha1.Size]byte

	logger *slog.Logger

	downloadedPieceCh chan downloadedPiece
	errorCh           chan error
	failedPiecesCh    chan piece
	incomingPeersCh   chan []*peer

	// Channel to signal the initial discovery of peers
	peersDiscoveredCh chan struct{}
	// Ensures the "peersDiscovered" channel is closed once
	peersDiscoveredOnce sync.Once

	// Channel to track all pending requests for more peers
	pendingPeerRequests   map[string]chan struct{}
	pendingPeerRequestsMu sync.Mutex

	// Channel to trigger the announcer routine manually
	triggerAnnounceCh chan struct{}

	metadataDownloadCompletedCh chan struct{}
	piecesDownloadCompleteCh    chan struct{}
	queuedPiecesCh              chan piece

	statusCh  chan torrentStatus
	stoppedCh chan struct{}

	err error

	trackers utils.Set

	peers map[string]*peer

	downloaded         int
	downloadedPieces   int
	maxPeerConnections int

	peersMu  sync.Mutex
	statusMu sync.Mutex

	peerId    [20]byte
	outputDir string
	status    torrentStatus
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

	for i := 0; i < len(tr.info.pieces); {
		select {
		case <-ctx.Done():
			return

		case failedPiece := <-tr.failedPiecesCh:
			tr.queuedPiecesCh <- failedPiece

		case tr.queuedPiecesCh <- tr.info.pieces[i]:
			i++
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

func (tr *Torrent) handleIncomingPeers(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return

		case peers := <-tr.incomingPeersCh:
			{
				for _, peer := range peers {
					if _, ok := tr.peers[peer.ipAddress]; ok {
						continue
					}

					// todo: make max number of peers configurable
					if tr.numOfPeers() <= 30 {
						tr.addPeer(peer)
					}

					tr.signalAllPendingRequests()

					tr.peersDiscoveredOnce.Do(func() {
						close(tr.peersDiscoveredCh)
					})
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
				switch status {
				case completed:
					log.Print("torrent download has completed.")

				case connecting:
					log.Print("connecting to peers...")

				case downloading:
					log.Print("downloading torrent files...")

				case downloadingMetadata:
					log.Print("downloading metadata...")

				case errored:
					log.Printf("torrent failed to download due to an error: %v", tr.err)

				case finished:
					log.Print("finished downloading all pieces")

				case flushing:
					log.Print("writing downloaded pieces to disk...")

				case stopped:
					log.Print("stopping torrent...")
				}
			}
		}

	}
}

func (tr *Torrent) printProgress() {
	var progress float64

	if tr.info.length > 0 {
		progress = float64(tr.downloaded) / float64(tr.info.length) * 100
	}

	log.Printf("(%.2f%%) downloaded %d of %d piece(s)\n", progress, tr.downloadedPieces, len(tr.info.pieces))
}

// Starts the announcer routine for the Torrent instance.
//
// This routine periodically sends announce requests to all trackers associated with the Torrent.
// It manages concurrent requests to trackers with a maximum concurrency limit, and dynamically
// adjusts the announce interval based on tracker responses. The function listens for context
// cancellation to gracefully stop announcing, and attempts to notify the Torrent of new peers
// received from tracker responses. If an announce request fails, it logs the error for debugging.
// The function ensures proper synchronization when updating the announce interval and cleans up
// resources when stopped.
func (tr *Torrent) startAnnouncer(ctx context.Context) {
	var intervalMu sync.Mutex

	interval := 5 * time.Second
	ticker := time.NewTicker(interval)

	tr.updateStatus(connecting)

	announce := func() {
		var wg sync.WaitGroup
		maxConcurrency := 5
		sem := utils.NewSemaphore(maxConcurrency)

		for trackerUrl := range tr.trackers.Entries() {
			select {
			case <-ctx.Done():
				return
			default:
			}

			sem.Acquire()
			wg.Add(1)

			go func(url string) {
				defer sem.Release()
				defer wg.Done()

				response, err := tr.sendAnnounceRequest(url)

				if err != nil {
					// todo: handle failed trackers
					tr.logger.Debug(fmt.Sprintf("announce request failed for tracker '%s': %v", url, err))
					return
				}

				if announcedInterval := time.Second * time.Duration(response.interval); announcedInterval > interval {
					intervalMu.Lock()
					interval = announcedInterval
					ticker.Reset(interval)
					intervalMu.Unlock()
				}

				select {
				case <-ctx.Done():
					return

				case tr.incomingPeersCh <- response.peers:
				}
			}(trackerUrl)
		}

		wg.Wait()
	}

	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// todo: notify trackers that we're stopping?
			return

		case <-ticker.C:
			announce()

		case <-tr.triggerAnnounceCh:
			// avoid unnecessary announce requests if there are no pending requests for more peers.
			// this scenario can happen if the manual trigger was sent while the "announce" routine was already running.
			tr.pendingPeerRequestsMu.Lock()
			numOfPendingReqs := len(tr.pendingPeerRequests)
			tr.pendingPeerRequestsMu.Unlock()

			if numOfPendingReqs < 1 {
				break
			}

			announce()
		}
	}
}

// Downloads the torrent's metadata (info) if it hasn't been downloaded yet.
//
// Initiates the process of downloading the torrent's metadata from peers.
// It repeatedly attempts to connect to idle peers that have not previously failed, establishes a peer connection,
// and checks for support of the metadata extension. If a peer supports the extension, it downloads the metadata,
// verifies its hash against the expected infoHash, and decodes the metadata. Upon successful parsing and validation,
// the torrent's "info" is set and the metadata download is marked as completed. The function handles errors gracefully,
// marking peers as idle and closing connections as needed. The process continues until metadata is successfully downloaded
// or the provided context is cancelled.
func (tr *Torrent) startMetadataDownloader(ctx context.Context) {
	tr.updateStatus(downloadingMetadata)

	markAsCompleted := func() {
		select {
		case <-tr.metadataDownloadCompletedCh:
		default:
			close(tr.metadataDownloadCompletedCh)
		}
	}

	if tr.info != nil {
		markAsCompleted()
		return
	}

	failedPeers := utils.NewSet()

	onExit := func(p *peer, pc *peerConnection, err error) {
		if err != nil {
			failedPeers.Add(p.socket)
			tr.logger.Debug(err.Error())
		}

		if pc != nil {
			pc.close()
		}

		p.markAsIdle()
	}

	for {
		select {
		case <-ctx.Done():
			return

		default:
			pr := tr.getIdlePeer(func(p *peer) bool {
				return !failedPeers.Has(p.socket)
			})

			if pr == nil {
				tr.requestMorePeers(ctx)
				break
			}

			pc := newPeerConnection(peerConnectionOpts{
				infoHash:         tr.infoHash,
				logger:           tr.logger,
				peerId:           tr.peerId,
				remotePeerSocket: pr.socket,
			})

			if err := pc.initConnection(peerConnectionInitConfig{bitfieldSize: 0}); err != nil {
				onExit(pr, pc, err)
				break
			}

			if !pc.supportsExtension(utMetadata) {
				onExit(pr, pc, fmt.Errorf("peer connection \"%s\" does not support the metadata extension", pc.remotePeerSocket))
				break
			}

			metadata, err := pc.downloadMetadata()

			if err != nil {
				onExit(pr, pc, err)
				break
			}

			if metadataHash := sha1.Sum(metadata); !bytes.Equal(tr.infoHash[:], metadataHash[:]) {
				// todo: blacklist peer?
				onExit(pr, pc, fmt.Errorf("metadata hash mismatch for peer \"%s\"; expected \"%x\", got \"%x\"", pc.remotePeerSocket, tr.infoHash, metadataHash))
				break
			}

			decodedValue, _, err := bencode.DecodeValue(metadata)

			if err != nil {
				onExit(pr, pc, err)
				break
			}

			metadataDict, ok := decodedValue.(map[string]any)

			if !ok {
				onExit(pr, pc, fmt.Errorf("expected decoded metadata to be a dictionary received %T", metadataDict))
				break
			}

			info, err := parseInfoDict(metadataDict)

			if err != nil {
				onExit(pr, pc, fmt.Errorf("failed to parse metadata from peer \"%s\": %w", pc.remotePeerSocket, err))
				break
			}

			tr.info = info
			onExit(pr, pc, nil)
			markAsCompleted()
			return
		}
	}
}

// todo: reduce function complexity
func (tr *Torrent) startPieceDownloader(ctx context.Context) {
	maxConcurrency := 5
	sem := utils.NewSemaphore(maxConcurrency)

	for {
		select {
		case <-ctx.Done():
			return

		default:
			{
				sem.Acquire()
				pr := tr.getIdlePeer(nil)

				select {
				case <-ctx.Done():
					sem.Release()
					return

				default:
					if pr == nil {
						// todo: request for more peers from trackers
						time.Sleep(2 * time.Second)
						sem.Release()
						break
					}

					tr.updateStatus(downloading)

					go func(p *peer, tr *Torrent) {
						defer sem.Release()

						pc := newPeerConnection(peerConnectionOpts{
							infoHash:         tr.infoHash,
							logger:           tr.logger,
							peerId:           tr.peerId,
							remotePeerSocket: p.socket,
						})

						if err := pc.initConnection(peerConnectionInitConfig{bitfieldSize: len(tr.info.pieces)}); err != nil {
							tr.logger.Debug(err.Error())
							tr.removePeer(pc.remotePeerSocket)
							return
						}

						defer pc.close()

						for {
							select {
							case <-ctx.Done():
								{
									return
								}

							case piece := <-tr.queuedPiecesCh:
								{
									downloadedPiece, err := pc.downloadPiece(piece)

									if err != nil && pc.failedAttempts >= peerConnectionMaxFailedAttempts {
										// todo: send to 'failedPeersCh"
										tr.failedPiecesCh <- piece
										tr.removePeer(pc.remotePeerSocket)
										return
									}

									if err != nil {
										tr.logger.Debug(err.Error())
										pc.failedAttempts += 1
										tr.failedPiecesCh <- piece
										continue
									}

									if err := downloadedPiece.validateIntegrity(); err != nil {
										// todo: implement hash fail threshold
										tr.logger.Debug(err.Error())
										pc.failedAttempts += 1
										tr.failedPiecesCh <- piece
										continue
									}

									tr.downloadedPieceCh <- *downloadedPiece
								}
							}
						}
					}(pr, tr)
				}
			}
		}
	}
}

func (tr *Torrent) startPieceWriter(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return

		case downloadedPiece := <-tr.downloadedPieceCh:
			{
				for _, fileIndex := range downloadedPiece.piece.fileIndexes {
					file := tr.info.files[fileIndex]
					fptr, err := file.openOrCreate(tr.outputDir)

					if err != nil {
						tr.errorCh <- err
						return
					}

					defer fptr.Close()

					fileOffset := int64(((downloadedPiece.piece.index - file.pieceStartIndex) * tr.info.pieceLength) - file.startOffsetInFirstPiece)

					pieceStartOffset := 0
					pieceEndOffset := downloadedPiece.piece.length

					isFirstPiece := file.pieceStartIndex == downloadedPiece.piece.index
					isFinalPiece := file.pieceEndIndex == downloadedPiece.piece.index

					if isFirstPiece {
						fileOffset = 0
						pieceStartOffset = file.startOffsetInFirstPiece
					}

					if isFinalPiece {
						pieceEndOffset = file.endOffsetInLastPiece
					}

					writeLen := pieceEndOffset - pieceStartOffset

					if fileOffset+int64(writeLen) > int64(file.length) {
						writeLen = int(file.length) - int(fileOffset)
					}

					if writeLen <= 0 {
						tr.errorCh <- fmt.Errorf("skipping write: calculated writeLen <= 0 for file %q (fileOffset=%d, pieceStartOffset=%d, pieceEndOffset=%d, file.length=%d, piece.index=%d)", file.name, fileOffset, pieceStartOffset, pieceEndOffset, file.length, downloadedPiece.piece.index)
						return
					}

					if _, err := fptr.WriteAt(downloadedPiece.data[pieceStartOffset:pieceStartOffset+writeLen], fileOffset); err != nil {
						tr.errorCh <- err
						return
					}
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
	tr.statusMu.Lock()
	defer tr.statusMu.Unlock()

	if tr.status != status {
		tr.status = status
		tr.statusCh <- status
	}
}

func (tr *Torrent) ID() string {
	return fmt.Sprintf("%x", tr.infoHash)
}

// Initializes and manages the lifecycle of the torrent download process.
//
// It sets up various goroutines to handle peer connections, metadata downloading,
// piece downloading, and writing. It also ensures
// that the torrent's metadata is downloaded before starting the piece download
// process. The function uses context objects to manage the cancellation of
// goroutines and ensures proper cleanup of resources upon shutdown.
func (t *Torrent) Start() {
	if t.status != initializing && t.status != stopped {
		t.logger.Debug(fmt.Sprintf("[%s]: torrent is already running", t.ID()))
		return
	}

	ctx, cancelCtx := context.WithCancel(context.Background())
	downloaderCtx, cancelDownloader := context.WithCancel(ctx)

	shutdownFn := func(status torrentStatus, err error) {
		if err != nil {
			t.err = err
		}

		t.updateStatus(status)
		cancelDownloader()
		cancelCtx()
	}

	t.downloaded = 0
	t.downloadedPieces = 0

	t.err = nil
	t.errorCh = make(chan error, 1)

	t.peersDiscoveredCh = make(chan struct{}, 1)
	t.peersDiscoveredOnce = sync.Once{}

	t.stoppedCh = make(chan struct{}, 1)

	go t.startAnnouncer(ctx)
	go t.handleStatusUpdate(ctx)
	go t.handleIncomingPeers(ctx)

	select {
	case err := <-t.errorCh:
		shutdownFn(errored, err)
		return

	case <-t.stoppedCh:
		shutdownFn(stopped, nil)
		return

	case <-t.peersDiscoveredCh:
		go t.startMetadataDownloader(ctx)
	}

	select {
	case err := <-t.errorCh:
		shutdownFn(errored, err)
		return

	case <-t.stoppedCh:
		shutdownFn(stopped, nil)
		return

	case <-t.metadataDownloadCompletedCh:
		go t.enqueuePieces(downloaderCtx)
		go t.startPieceDownloader(downloaderCtx)
		go t.startPieceWriter(downloaderCtx)
	}

	select {
	case err := <-t.errorCh:
		shutdownFn(errored, err)
		return

	case <-t.stoppedCh:
		shutdownFn(stopped, nil)

	case <-t.piecesDownloadCompleteCh:
		shutdownFn(completed, nil)
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
