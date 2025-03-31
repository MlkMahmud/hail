package client

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/sync/errgroup"

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

	numOfPeers := len(peers)
	numOfPiecesProcessed := 0
	numOfPiecesToDownload := len(trrnt.Info.Pieces)

	downloadedPieces := make(chan torrent.DownloadedPiece, numOfPiecesToDownload)
	piecesTodownload := make(chan torrent.Piece, numOfPiecesToDownload)

	g := new(errgroup.Group)
	var mutex sync.Mutex

	for i := 0; i < numOfPiecesToDownload; i++ {
		piecesTodownload <- trrnt.Info.Pieces[i]

		if i < numOfPeers {
			go func() {
				peerConnection := torrent.NewPeerConnection(torrent.PeerConnectionConfig{DoExtensionHandshake: true, InfoHash: trrnt.InfoHash, Peer: peers[i]})

				for piece := range piecesTodownload {
					downloadedPiece, err := peerConnection.DownloadPiece(piece)

					if err != nil {
						fmt.Print(err)
						piecesTodownload <- piece
					}

					if err := downloadedPiece.CheckHashIntegrity(); err != nil {
						fmt.Print(err)
						piecesTodownload <- piece
					}

					downloadedPieces <- *downloadedPiece
				}
			}()
		}

		if i < numOfPiecesToDownload/2 {
			g.Go(func() error {
				mutex.Lock()

				if numOfPiecesProcessed == numOfPiecesToDownload {
					mutex.Unlock()
					close(piecesTodownload)
					close(downloadedPieces)
					return nil
				}

				for downloadedPiece := range downloadedPieces {
					file := filepath.Join(tempDir, fmt.Sprintf("%020d.piece", downloadedPiece.Piece.Index))

					err := os.WriteFile(file, downloadedPiece.Data, 0666)

					mutex.Lock()
					numOfPiecesProcessed += 1
					mutex.Unlock()

					if err != nil {
						return err
					}
				}

				return nil
			})
		}
	}

	if err := g.Wait(); err != nil {
		return fmt.Errorf("failed to download torrent: %w", err)
	}

	if err := utils.MergeSortedDirectoryToFile(tempDir, dest); err != nil {
		return fmt.Errorf("failed to download torrent: %w", err)
	}

	return nil
}
