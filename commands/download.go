package commands

import (
	"fmt"
	"os"
	"strconv"

	"github.com/MlkMahmud/hail/client"
	"github.com/MlkMahmud/hail/torrent"
	"github.com/urfave/cli/v2"
)

func HandleDownload(ctx *cli.Context) error {
	dest := ctx.String("out_path")
	src := ctx.Args().First()

	if err := client.Download(src, dest); err != nil {
		return err
	}

	return nil
}

func HandleDownloadPiece(ctx *cli.Context) error {
	dest := ctx.String("out_path")
	src := ctx.Args().Get(0)
	pieceIndex, err := strconv.ParseUint(ctx.Args().Get(1), 10, 64)

	if err != nil {
		return fmt.Errorf("piece index must be a positive 64 Bit integer")
	}

	trrnt, err := torrent.NewTorrent(src)

	if err != nil {
		return err
	}

	peers, err := trrnt.GetPeers()

	if err != nil {
		return err
	}

	if err := trrnt.DownloadMetadata(); err != nil {
		return err
	}

	if numOfPieces := len(trrnt.Info.Pieces); pieceIndex >= uint64(numOfPieces) {
		return fmt.Errorf("piece index is out of bounds. torrent contains only %d pieces", numOfPieces)
	}

	var downloadedPiece *torrent.DownloadedPiece
	var downloadErr error

	for _, peer := range peers {
		peerConnection := torrent.NewPeerConnection(torrent.PeerConnectionConfig{Peer: peer})
		downloadedPiece, downloadErr = peerConnection.DownloadPiece(trrnt.Info.Pieces[pieceIndex])

		if downloadErr == nil {
			break
		}
	}

	if downloadErr != nil {
		return downloadErr
	}

	if err := os.WriteFile(dest, downloadedPiece.Data, 0644); err != nil {
		return err
	}

	return nil
}
