package commands

import (
	"github.com/MlkMahmud/hail/torrent"
	"github.com/urfave/cli/v2"
)

func HandleDownloadCommand(ctx *cli.Context) error {
	src := ctx.Args().First()

	trrnt, err := torrent.NewTorrent(src)

	if err != nil {
		return err
	}

	trrnt.Start()

	return nil
}
