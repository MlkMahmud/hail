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
)

type NewTorrentOpts struct {
	Logger    *slog.Logger
	PeerId    [20]byte
	OutputDir string
	Src       string
}

// todo: use mutexes to wrap properties accessed by multiple goroutines
type Torrent struct {
	info *torrentInfo

	infoHash [sha1.Size]byte

	logger *slog.Logger

	bannedPeersCh               chan string
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

	metadataDownloadCompletedMu sync.Mutex
	statusMu                    sync.Mutex

	peerId                    [20]byte
	metadataDownloadCompleted bool
	metadataPeersCh           chan *peerConnection
	peerConnectionPool        *peerConnectionPool
	outputDir                 string
	status                    torrentStatus
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
						logger:     tr.logger,
						peerId:     tr.peerId,
						remotePeer: peer,
					})

					// todo: place a limit on the number of idle peers
					tr.peers[peer.String()] = peer

					if tr.peerConnectionPool.size() < tr.maxPeerConnections {
						tr.peerConnectionPool.addConnection(pc)
					}

					select {
					case <-tr.metadataDownloadCompletedCh:
						continue
					case tr.metadataPeersCh <- pc:
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
				switch status {
				case completed:
					log.Print("torrent download has completed.")
				case connecting:
					log.Print("connecting to peers...")
				case downloading:
					log.Print("downloading torrent files...")
				case downloadingMetadata:
					log.Print("downloading metadata...")
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
				if err := pc.initConnection(peerConnectionInitConfig{bitfieldSize: 0}); err != nil {
					tr.logger.Debug(err.Error())
					break
				}

				defer pc.close()

				if !pc.supportsExtension(utMetadata) {
					tr.logger.Debug(fmt.Sprintf("peer connection \"%s\" does not support the metadata extension\n", pc.remotePeerAddress))
					break
				}

				tr.updateStatus(downloadingMetadata)

				metadata, err := pc.downloadMetadata()

				if err != nil {
					tr.logger.Debug(err.Error())
					break
				}

				if metadataHash := sha1.Sum(metadata); !bytes.Equal(tr.infoHash[:], metadataHash[:]) {
					// todo: blacklist peer?
					tr.logger.Debug(
						fmt.Sprintf("metadata hash mismatch for peer \"%s\"; expected \"%x\", got \"%x\"", pc.remotePeerAddress, tr.infoHash, metadataHash),
					)

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
					tr.logger.Debug(
						fmt.Sprintf("failed to parse metadata info dictionary from peer \"%s\": %v", pc.remotePeerAddress, err),
					)
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
						tr.logger.Debug(err.Error())
						sem.Release()
						break
					}

					tr.updateStatus(downloading)

					go func(pc *peerConnection, tr *Torrent) {
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
										tr.logger.Debug(err.Error())
										tr.failedPiecesCh <- piece
										tr.peerConnectionPool.removeConnection(pc.remotePeerAddress)
										return
									}

									downloadedPiece, err := pc.downloadPiece(piece)

									if err != nil && pc.failedAttempts >= peerConnectionMaxFailedAttempts {
										// todo: send to 'failedPeersCh"
										tr.failedPiecesCh <- piece
										tr.peerConnectionPool.removeConnection(pc.remotePeerAddress)
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
					}(conn, tr)
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

					//todo: handle error
					if err != nil {
						tr.logger.Debug(err.Error())
						continue
					}

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
						// todo: this condition should never evaluate to true, if it does throw an error
						tr.logger.Debug(
							fmt.Sprintf("skipping write: calculated writeLen <= 0 for file %q (fileOffset=%d, pieceStartOffset=%d, pieceEndOffset=%d, file.length=%d, piece.index=%d)", file.name, fileOffset, pieceStartOffset, pieceEndOffset, file.length, downloadedPiece.piece.index),
						)

						continue
					}

					if _, err := fptr.WriteAt(downloadedPiece.data[pieceStartOffset:pieceStartOffset+writeLen], fileOffset); err != nil {
						// todo: properly handle this error
						tr.logger.Debug(err.Error())
					}

					fptr.Close()
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
	ctx, cancelCtx := context.WithCancel(context.Background())
	downloaderCtx, cancelDownloader := context.WithCancel(ctx)

	shutdownFn := func(status torrentStatus) {
		t.updateStatus(status)
		cancelDownloader()
		cancelCtx()
		t.peerConnectionPool.closeConnections()
	}

	t.downloaded = 0
	t.downloadedPieces = 0
	t.stoppedCh = make(chan struct{}, 1)

	go t.handleStatusUpdate(ctx)

	go t.startAnnouncer(ctx)
	go t.handleIncomingPeers(ctx)
	go t.handleBannedPeers(ctx)
	go t.startMetadataDownloader(ctx)

	select {
	case <-t.stoppedCh:
		shutdownFn(stopped)
		return

	case <-t.metadataDownloadCompletedCh:
		go t.enqueuePieces(downloaderCtx)
		go t.startPieceDownloader(downloaderCtx)
		go t.startPieceWriter(downloaderCtx)
	}

	select {
	case <-t.stoppedCh:
		shutdownFn(stopped)

	case <-t.piecesDownloadCompleteCh:
		shutdownFn(completed)
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
