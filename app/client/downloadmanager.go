package client

import (
	"crypto/sha1"
	"fmt"
	"runtime"
	"sync"

	"github.com/codecrafters-io/bittorrent-starter-go/app/torrent"
)

type downloadManager struct {
	activeDownload  download
	connections     map[string]torrent.PeerConnection
	mutex           sync.Mutex
	queuedDownloads []download
}

type download struct {
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

const (
	maxNumOfPeerConnections = 30
)

func newDownloadManager(peers []torrent.Peer) *downloadManager {
	return new(downloadManager)
}

func (dm *downloadManager) addPeerConnectionToPool(peerConnection torrent.PeerConnection) {
	dm.mutex.Lock()
	dm.connections[peerConnection.PeerAddress] = peerConnection
	dm.mutex.Unlock()

	return
}

func (dm *downloadManager) dequeueDownload() *download {
	if len(dm.queuedDownloads) == 0 {
		return nil
	}

	dequeuedDownload := dm.queuedDownloads[0]
	dm.queuedDownloads = dm.queuedDownloads[1:]

	return &dequeuedDownload
}

func (dm *downloadManager) enqueueDownload(download download) {
	dm.queuedDownloads = append(dm.queuedDownloads, download)
}

func (dm *downloadManager) numOfPeerConnectionsInPool() int {
	dm.mutex.Lock()
	defer dm.mutex.Unlock()
	num := len(dm.connections)
	return num
}

func (dm *downloadManager) removePeerConnectionFromPool(peerAddress string) {
	dm.mutex.Lock()
	delete(dm.connections, peerAddress)
	dm.mutex.Unlock()
}

func (dm *downloadManager) initPeerConnectionPool(peers []torrent.Peer) {
	peerConnectionPoolSize := min(len(peers), 2*runtime.NumCPU(), maxNumOfPeerConnections)

	for i := range peerConnectionPoolSize {
		peerConnection := torrent.NewPeerConnection(torrent.PeerConnectionConfig{Peer: peers[i]})
		dm.connections[peerConnection.PeerAddress] = *peerConnection
	}

	return
}

func (dm *downloadManager) startDownload(config peerConnectionPoolDownloadConfig) {
	for _, peerConnection := range dm.connections {
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
