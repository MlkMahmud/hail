package client

import (
	"crypto/sha1"
	"fmt"
	"os"
	"path/filepath"

	"github.com/codecrafters-io/bittorrent-starter-go/app/torrent"
	"github.com/codecrafters-io/bittorrent-starter-go/app/utils"
)

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

	// numOfPeers := len(peers)
	numOfPiecesProcessed := 0
	numOfPiecesToDownload := len(trrnt.Info.Pieces)

	downloadedPieces := make(chan torrent.DownloadedPiece, numOfPiecesToDownload)
	// piecesTodownload := make(chan torrent.Piece, numOfPiecesToDownload)
	requestBatchSize := 3

	for numOfPiecesProcessed < numOfPiecesToDownload {
		pendingPieces := trrnt.Info.Pieces[numOfPiecesProcessed:]
		numOfPendingPieces := len(pendingPieces)
		currentBatchSize := min(requestBatchSize, numOfPendingPieces)

		for i := 0; i < currentBatchSize; i++ {
			go func(piece torrent.Piece, peer torrent.Peer, infoHash [sha1.Size]byte) {
				peerConnection := torrent.NewPeerConnection(torrent.PeerConnectionConfig{Peer: peers[i], InfoHash: trrnt.InfoHash})

				if err := peerConnection.InitConnection(); err != nil {
					downloadedPieces <- torrent.DownloadedPiece{Err: err}
					return
				}

				downloadedPiece, err := peerConnection.DownloadPiece(piece)

				if err != nil {
					downloadedPieces <- torrent.DownloadedPiece{Err: err}
					return
				}

				if err := downloadedPiece.CheckHashIntegrity(); err != nil {
					downloadedPieces <- torrent.DownloadedPiece{Err: err}
					return
				}

				downloadedPieces <- *downloadedPiece
				return
			}(pendingPieces[i], peers[i], trrnt.InfoHash)
		}

		for i := 0; i < currentBatchSize; i++ {
			downloadedPiece := <-downloadedPieces

			if downloadedPiece.Err != nil {
				return err
			}

			file := filepath.Join(tempDir, fmt.Sprintf("%020d.piece", downloadedPiece.Piece.Index))

			if err := os.WriteFile(file, downloadedPiece.Data, 0666); err != nil {
				return err
			}

			numOfPiecesProcessed += 1
		}
	}

	// for i := 0; i < numOfPiecesToDownload; i++ {
	// 	piecesTodownload <- trrnt.Info.Pieces[i]

	// 	if i < numOfPeers {
	// 		go func() {
	// 			peerConnection := torrent.NewPeerConnection(torrent.PeerConnectionConfig{DoExtensionHandshake: true, InfoHash: trrnt.InfoHash, Peer: peers[i]})

	// 			for piece := range piecesTodownload {
	// 				downloadedPiece, err := peerConnection.DownloadPiece(piece)

	// 				if err != nil {
	// 					fmt.Print(err)
	// 					piecesTodownload <- piece
	// 					continue
	// 				}

	// 				if err := downloadedPiece.CheckHashIntegrity(); err != nil {
	// 					fmt.Print(err)
	// 					piecesTodownload <- piece
	// 					continue
	// 				}

	// 				downloadedPieces <- *downloadedPiece
	// 			}
	// 		}()
	// 	}
	// }

	// for numOfPiecesProcessed < numOfPiecesToDownload {
	// 	downloadedPiece := <-downloadedPieces

	// 	if downloadedPiece.Err != nil {
	// 		return fmt.Errorf("failed to download torrent: %w", err)
	// 	}

	// 	file := filepath.Join(tempDir, fmt.Sprintf("%020d.piece", downloadedPiece.Piece.Index))

	// 	if err := os.WriteFile(file, downloadedPiece.Data, 0666); err != nil {
	// 		return fmt.Errorf("failed to download torrent: %w", err)
	// 	}
	// }

	if err := utils.MergeSortedDirectoryToFile(tempDir, dest); err != nil {
		return fmt.Errorf("failed to download torrent: %w", err)
	}

	return nil
}
