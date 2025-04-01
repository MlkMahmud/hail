package client

import (
	"crypto/sha1"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/codecrafters-io/bittorrent-starter-go/app/torrent"
	"github.com/codecrafters-io/bittorrent-starter-go/app/utils"
)

type downloadPoolConfig struct {
	downloadedPieces      chan torrent.DownloadedPiece
	numOfPiecesDownloaded *int
	numOfPiecesToDownload int
	peers                 []torrent.Peer
	piecesToDownload      chan torrent.Piece
	torrentInfoHash       [sha1.Size]byte
}

func addPeersToDownloadPool(config downloadPoolConfig) {
	var mutex sync.Mutex
	numOfPeerConnectionsInPool := 0
	peerConnectionPoolHasBeenDrained := false
	peerConnectionPoolSize := min(len(config.peers), 10)

	for i := 0; i < peerConnectionPoolSize; i++ {
		go func(p torrent.Peer) {
			peerConnection := torrent.NewPeerConnection(torrent.PeerConnectionConfig{InfoHash: config.torrentInfoHash, Peer: p})

			mutex.Lock()
			numOfPeerConnectionsInPool += 1
			mutex.Unlock()

			for piece := range config.piecesToDownload {
				downloadedPiece, err := peerConnection.DownloadPiece(piece)

				if err != nil && peerConnection.FailedAttempts >= torrent.MaxFailedAttempts {
					fmt.Println(err)
					config.piecesToDownload <- piece

					mutex.Lock()
					numOfPeerConnectionsInPool -= 1

					if numOfPeerConnectionsInPool == 0 {
						peerConnectionPoolHasBeenDrained = true
					}

					mutex.Unlock()
					return
				}

				if err != nil {
					fmt.Println(err)
					peerConnection.FailedAttempts += 1
					config.piecesToDownload <- piece
					continue
				}

				if err := downloadedPiece.CheckHashIntegrity(); err != nil {
					fmt.Println(err)
					peerConnection.FailedAttempts += 1
					config.piecesToDownload <- piece
					continue
				}

				config.downloadedPieces <- *downloadedPiece
				mutex.Lock()
				*config.numOfPiecesDownloaded += 1
				mutex.Unlock()
			}
		}(config.peers[i])
	}

	go func() {
		done := false

		for {
			mutex.Lock()
			if *config.numOfPiecesDownloaded == config.numOfPiecesToDownload || peerConnectionPoolHasBeenDrained {
				done = true
			}
			mutex.Unlock()

			if done {
				close(config.downloadedPieces)
				close(config.piecesToDownload)
				return
			}
		}
	}()
}

func Download(src string, dest string) error {
	trrnt, err := torrent.NewTorrent(src)

	if err != nil {
		return fmt.Errorf("failed to download torrent: %w", err)
	}

	peers, err := trrnt.GetPeers()

	if err != nil {
		return fmt.Errorf("failed to download torrent: %w", err)
	}

	var metadataRequestErr error

	if trrnt.Info.Pieces == nil {
		metadataRequestErr = trrnt.DownloadMetadata()
	}

	if metadataRequestErr != nil {
		return fmt.Errorf("failed to download torrent: %w", err)
	}

	tempDir, err := os.MkdirTemp("", trrnt.Info.Name)

	if err != nil {
		return err
	}

	defer os.RemoveAll(tempDir)

	numOfPiecesDownloaded := 0
	numOfPiecesToDownload := len(trrnt.Info.Pieces)

	downloadedPieces := make(chan torrent.DownloadedPiece, numOfPiecesToDownload)
	piecesTodownload := make(chan torrent.Piece, numOfPiecesToDownload)

	for i := 0; i < numOfPiecesToDownload; i++ {
		piecesTodownload <- trrnt.Info.Pieces[i]
	}

	addPeersToDownloadPool(downloadPoolConfig{
		downloadedPieces:      downloadedPieces,
		numOfPiecesDownloaded: &numOfPiecesDownloaded,
		numOfPiecesToDownload: numOfPiecesToDownload,
		peers:                 peers,
		piecesToDownload:      piecesTodownload,
		torrentInfoHash:       trrnt.InfoHash,
	})

	for downloadedPiece := range downloadedPieces {
		if downloadedPiece.Err != nil {
			return fmt.Errorf("failed to download torrent: %w", downloadedPiece.Err)
		}

		file := filepath.Join(tempDir, fmt.Sprintf("%020d.piece", downloadedPiece.Piece.Index))

		if err := os.WriteFile(file, downloadedPiece.Data, 0666); err != nil {
			return fmt.Errorf("failed to download torrent: %w", err)
		}
	}

	if numOfPiecesDownloaded != numOfPiecesToDownload {
		return fmt.Errorf("failed to download complete file. downloaded %d out of %d expected pieces", numOfPiecesDownloaded, numOfPiecesToDownload)
	}

	if err := utils.MergeSortedDirectoryToFile(tempDir, dest); err != nil {
		return fmt.Errorf("failed to download torrent: %w", err)
	}

	return nil
}
