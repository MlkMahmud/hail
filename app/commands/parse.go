package commands

import (
	"fmt"

	"github.com/codecrafters-io/bittorrent-starter-go/app/torrent"
	"github.com/urfave/cli/v2"
)

func HandleParseCommand(ctx *cli.Context) error {

	src := ctx.Args().First()

	trrnt, err := torrent.NewTorrent(src)

	if err != nil {
		return err
	}

	fmt.Printf("Tracker URL: %s\n", trrnt.TrackerUrl)
	fmt.Printf("Info Hash: %x\n", trrnt.InfoHash)

	return nil
}
