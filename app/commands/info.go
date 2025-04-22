package commands

import (
	"fmt"

	"github.com/codecrafters-io/bittorrent-starter-go/app/torrent"
	"github.com/urfave/cli/v2"
)

func HandleInfoCommand(ctx *cli.Context) error {
	src := ctx.Args().First()
	trrnt, err := torrent.NewTorrent(src)

	if err != nil {
		return err
	}

	// if err := trrnt.DownloadMetadata(); err != nil {
	// 	return err
	// }

	fmt.Printf("Tracker URL: %s\n", trrnt.TrackerUrl)
	// fmt.Printf("Length: %d\n", trrnt.Info.Length)
	// fmt.Printf("Info Hash: %x\n", trrnt.InfoHash)
	// fmt.Printf("Piece Length: %d\n", trrnt.Info.Pieces[0].Length)
	// fmt.Println("Piece Hashes:")
	// for _, piece := range trrnt.Info.Pieces {
	// 	fmt.Printf("%x\n", piece.Hash)
	// }

	for _, file := range trrnt.Info.Files {
		fmt.Printf("Name: %s\n", file.Name)
	}

	return nil
}
