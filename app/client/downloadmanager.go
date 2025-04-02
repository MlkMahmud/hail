package client

import (
	"crypto/sha1"
	"fmt"

	"github.com/codecrafters-io/bittorrent-starter-go/app/torrent"
)

type downloadManager struct {
	activeDownload     downloadRequest
	peerConnectionPool torrent.PeerConnectionPool
	downloadQueue      []downloadRequest
}
type downloadRequest struct {
	name   string
	peers  []torrent.Peer
	pieces []torrent.Piece
}

type peerConnectionPoolDownloadConfig struct {
	downloadedPieces      chan torrent.DownloadedPiece
	numOfPiecesDownloaded *int
	numOfPiecesToDownload int
	piecesToDownload      chan torrent.Piece
	torrentInfoHash       [sha1.Size]byte
}

func newDownloadManager(peers []torrent.Peer) *downloadManager {
	downloadManager := new(downloadManager)
	downloadManager.peerConnectionPool = *torrent.NewPeerConnectionPool()

	return downloadManager
}

func (dm *downloadManager) dequeue() *downloadRequest {
	if len(dm.downloadQueue) == 0 {
		return nil
	}

	dequeuedDownload := dm.downloadQueue[0]
	dm.downloadQueue = dm.downloadQueue[1:]

	return &dequeuedDownload
}

func (dm *downloadManager) enqueue(item downloadRequest) {
	dm.downloadQueue = append(dm.downloadQueue, download)
}

func (dm *downloadManager) startDownload() {
	dwnld := dm.dequeue()

	if dwnld == nil {
		fmt.Println("download manager queue is empty.")
		return
	}

	for _, peerConnection := range dm.peerConnectionPool.Connections {
		go func(p torrent.PeerConnection) {
			for piece := range config.piecesToDownload {
				downloadedPiece, err := p.DownloadPiece(piece)

				if err != nil && p.FailedAttempts >= torrent.MaxFailedAttempts {
					fmt.Println(err)
					config.piecesToDownload <- piece
					dm.removePeerConnectionFromPool(p.PeerAddress)
					return
				}

				if err != nil {
					fmt.Println(err)
					p.FailedAttempts += 1
					config.piecesToDownload <- piece
					continue
				}

				if err := downloadedPiece.CheckHashIntegrity(); err != nil {
					fmt.Println(err)
					p.FailedAttempts += 1
					config.piecesToDownload <- piece
					continue
				}

				config.downloadedPieces <- *downloadedPiece
				// update download progress
			}

		}(peerConnection)
	}
}
