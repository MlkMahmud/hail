package main

import (
	"log"
	"os"

	"github.com/codecrafters-io/bittorrent-starter-go/app/commands"
	"github.com/urfave/cli/v2"
)

func main() {
	app := &cli.App{
		Name: "Basic",
		Commands: []*cli.Command{
			{
				Name:   "decode",
				Action: commands.HandleDecode,
				Usage:  "decodes a BEncoded string.",
			},
			{
				Name:    "download",
				Action:  commands.HandleDownload,
				Aliases: []string{"magnet_download"},
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "out_path",
						Aliases:  []string{"o"},
						Required: true,
						Usage:    "destination for torrent download",
					},
				},
				Usage: "downloads a torrent",
			},
			{
				Name:    "download_piece",
				Action:  commands.HandleDownloadPiece,
				Aliases: []string{"magnet_download_piece"},
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "out_path",
						Aliases:  []string{"o"},
						Required: true,
						Usage:    "destination for torrent piece download",
					},
				},
				Usage: "downloads a single piece for the provided torrent",
			},
			{
				Name:    "handshake",
				Action:  commands.HandleHandshakeCommand,
				Aliases: []string{"magnet_handshake"},
				Usage:   "initiates a peer handshake and outputs information about the connected peer",
			},
			{
				Name:    "info",
				Action:  commands.HandleInfoCommand,
				Aliases: []string{"magnet_info"},
				Usage:   "outputs metadata for the provided torrent",
			},
			{
				Name:    "parse",
				Action:  commands.HandleParseCommand,
				Aliases: []string{"magnet_parse"},
				Usage:   "parses and outputs information for a provided torrent",
			},
			{
				Name:   "peers",
				Action: commands.HandlePeersCommand,
				Usage:  "outputs a list of peers for the provided torrent",
			},
		},
		Description: "A basic BitTorrent client",
		Usage:       "Download all your favourite torrents.",
	}

	if err := app.Run(os.Args); err != nil {
		log.Fatal(err)
	}
}
