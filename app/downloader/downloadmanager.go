package downloader

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/codecrafters-io/bittorrent-starter-go/app/torrent"
)

type DownloadManager struct {
	downloadQueue []DownloadRequest
}

type DownloadRequest struct {
	completed chan bool
	failed    chan bool

	dest                  string
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

type DownloadRequestConfig struct {
	Dest   string
	Name   string
	Peers  []torrent.Peer
	Pieces []torrent.Piece
}

func NewDownloadManager() *DownloadManager {
	return new(DownloadManager)
}

func NewDownloadRequest(config DownloadRequestConfig) *DownloadRequest {
	var mutex sync.Mutex
	var once sync.Once

	return &DownloadRequest{
		completed: make(chan bool),
		failed:    make(chan bool),

		dest:                  config.Dest,
		name:                  config.Name,
		numOfPiecesDownloaded: 0,
		numOfPiecesToDownload: len(config.Pieces),
		peers:                 config.Peers,
		pieces:                config.Pieces,

		mutex: &mutex,
		once:  &once,
	}
}

func (dr *DownloadRequest) markAsCompleted() {
	dr.once.Do(func() {
		dr.completed <- true
	})
}

func (dr *DownloadRequest) markAsFailed() {
	dr.once.Do(func() {
		dr.failed <- true
	})
}

func (dr *DownloadRequest) mergeDownloadedPieces() error {
	// todo: check if dest is a directory
	destFile, err := os.OpenFile(dr.dest, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0666)

	if err != nil {
		return err
	}

	defer destFile.Close()

	entries, err := os.ReadDir(dr.tempDir)

	if err != nil {
		return err
	}

	for _, entry := range entries {
		path := filepath.Join(dr.tempDir, entry.Name())
		contents, err := os.ReadFile(path)

		if err != nil {
			return nil
		}

		if _, err := destFile.Write(contents); err != nil {
			return err
		}
	}

	return nil
}

func (dr *DownloadRequest) startDownload() {
	// return early if peers list is empty or there are no pieces to be downloaded
	tempDir, err := os.MkdirTemp("", "")

	if err != nil {
		// handle error and move to next item
		fmt.Println(err)
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
					{
						return
					}
				case piece := <-piecesToDownload:
					downloadedPiece, err := pc.DownloadPiece(piece)

					if err != nil && pc.FailedAttempts >= torrent.MaxFailedAttempts {
						fmt.Println(err)
						piecesToDownload <- piece
						connectionPool.RemovePeerConnectionFromPool(pc.PeerAddress, dr.markAsFailed)
						return
					}

					if err != nil {
						fmt.Println(err)
						pc.FailedAttempts += 1
						piecesToDownload <- piece
						continue
					}

					if err := downloadedPiece.CheckHashIntegrity(); err != nil {
						fmt.Println(err)
						pc.FailedAttempts += 1
						piecesToDownload <- piece
						continue
					}

					if err := downloadedPiece.WriteToDisk(dr.tempDir); err != nil {
						fmt.Println(err)
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

func (dr *DownloadRequest) updateProgress() {
	dr.mutex.Lock()
	dr.numOfPiecesDownloaded = min(dr.numOfPiecesDownloaded+1, dr.numOfPiecesToDownload)

	defer dr.mutex.Unlock()

	if dr.numOfPiecesDownloaded == dr.numOfPiecesToDownload {
		dr.markAsCompleted()
		return
	}
}

func (dr *DownloadRequest) waitForCompletion() error {
	select {
	case <-dr.completed:
		{
			dr.mergeDownloadedPieces()
			dr.cancelFunc()
			return nil
		}

	case <-dr.failed:
		{
			dr.cancelFunc()
			// todo: find a way to return an error here
			return nil

		}
	}
}

func (dm *DownloadManager) Dequeue() *DownloadRequest {
	if len(dm.downloadQueue) == 0 {
		return nil
	}

	dequeuedDownload := dm.downloadQueue[0]
	dm.downloadQueue = dm.downloadQueue[1:]

	return &dequeuedDownload
}

func (dm *DownloadManager) Enqueue(req *DownloadRequest) {
	dm.downloadQueue = append(dm.downloadQueue, *req)
}

func (dm *DownloadManager) Start() {
	for req := dm.Dequeue(); req != nil; req = dm.Dequeue() {
		// todo: download multiple files concurrently ?
		req.startDownload()
	}
}
