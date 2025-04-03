package client

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/codecrafters-io/bittorrent-starter-go/app/torrent"
)

type downloadManager struct {
	downloadQueue      []downloadRequest
}

type downloadRequest struct {
	completed chan bool
	failed    chan bool

	name                  string
	numOfPiecesDownloaded int
	numOfPiecesToDownload int
	peers                 []torrent.Peer
	pieces                []torrent.Piece
	tempDir               string

	context    context.Context
	cancelFunc context.CancelFunc

	mutex *sync.Mutex
	once  *sync.Once
}

func newDownloadManager(peers []torrent.Peer) *downloadManager {
	downloadManager := new(downloadManager)

	return downloadManager
}

func newDowloadRequest(name string, peers []torrent.Peer, pieces []torrent.Piece) *downloadRequest {
	var mutex sync.Mutex
	var once sync.Once

	return &downloadRequest{
		completed: make(chan bool),
		failed:    make(chan bool),

		name:                  name,
		numOfPiecesDownloaded: 0,
		numOfPiecesToDownload: len(pieces),
		peers:                 peers,
		pieces:                pieces,

		mutex: &mutex,
		once:  &once,
	}
}

func (dr *downloadRequest) markAsCompleted() {
	dr.once.Do(func() {
		dr.completed <- true
	})
}

func (dr *downloadRequest) startDownload() {
	// return early if peers list is empty or there are no pieces to be downloaded

	tempDir, err := os.MkdirTemp("", "")

	if err != nil {
		// handle error and move to next item
	}

	dr.tempDir = tempDir
	dr.context, dr.cancelFunc = context.WithCancel(context.Background())

	connectionPool := torrent.NewPeerConnectionPool()
	connectionPool.InitPeerConnectionPool(dr.peers)

	piecesToDownload := make(chan torrent.Piece, dr.numOfPiecesToDownload)

	for i := range dr.numOfPiecesToDownload {
		piecesToDownload <- dr.pieces[i]
	}

	for _, connection := range connectionPool.Connections {
		go func(ct context.Context, pc torrent.PeerConnection) {
			for {
				select {
				case <-ct.Done():
					return
				case piece := <-piecesToDownload:
					downloadedPiece, err := pc.DownloadPiece(piece)

					if err != nil && pc.FailedAttempts >= torrent.MaxFailedAttempts {
						piecesToDownload <- piece
						connectionPool.RemovePeerConnectionFromPool(pc.PeerAddress)
						return
					}

					if err != nil {
						pc.FailedAttempts += 1
						piecesToDownload <- piece
						continue
					}

					if err := downloadedPiece.CheckHashIntegrity(); err != nil {
						pc.FailedAttempts += 1
						piecesToDownload <- piece
						continue
					}

					if err := downloadedPiece.WriteToDisk(dr.tempDir); err != nil {
						pc.FailedAttempts += 1
						// todo: parse error to see if it's worth retrying.
						piecesToDownload <- piece
						continue
					}

					dr.updateProgress()
				}
			}
		}(dr.context, connection)
	}

	if err := dr.waitForCompletion(); err != nil {
		// todo: collect errors and return to caller when function is done
		fmt.Println(err)
	}

	defer os.RemoveAll(dr.tempDir)
}

func (dr *downloadRequest) updateProgress() {
	dr.mutex.Lock()
	dr.numOfPiecesDownloaded = min(dr.numOfPiecesDownloaded, dr.numOfPiecesDownloaded+1)

	defer dr.mutex.Unlock()

	if dr.numOfPiecesDownloaded == dr.numOfPiecesToDownload {
		dr.markAsCompleted()
		return
	}
}

func (dr *downloadRequest) waitForCompletion() error {
	select {
	case <-dr.completed:
		dr.cancelFunc()
		return nil

	case <-dr.failed:
		dr.cancelFunc()
		// todo: find a way to return an error here
		return nil
	}
}

func (dm *downloadManager) dequeue() *downloadRequest {
	if len(dm.downloadQueue) == 0 {
		return nil
	}

	dequeuedDownload := dm.downloadQueue[0]
	dm.downloadQueue = dm.downloadQueue[1:]

	return &dequeuedDownload
}

func (dm *downloadManager) enqueue(req *downloadRequest) {
	dm.downloadQueue = append(dm.downloadQueue, *req)
}

func (dm *downloadManager) start() {
	for req := dm.dequeue(); req != nil; req = dm.dequeue() {
		// todo: download multiple files concurrently ?
		req.startDownload()
	}
}
