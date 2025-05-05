package main

import (
	"log"
	"os"

	"github.com/MlkMahmud/hail/commands"
	"github.com/urfave/cli/v2"
)

func main() {
	app := &cli.App{
		Name: "Basic",
		Commands: []*cli.Command{
			{
				Name:    "download",
				Action:  commands.HandleDownloadCommand,
				Aliases: []string{"magnet_download"},
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "out_path",
						Aliases:  []string{"o"},
						Required: true,
						Usage:    "destination for torrent download",
					},
				},
				Usage:     "downloads a torrent",
				UsageText: "Basic download -o <value> <torrent>",
			},
		},
		Description: "A basic BitTorrent client",
		Usage:       "Download all your favourite torrents.",
	}

	if err := app.Run(os.Args); err != nil {
		log.Fatal(err)
	}
}
