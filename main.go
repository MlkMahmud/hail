package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/MlkMahmud/hail/session"
	"github.com/MlkMahmud/hail/utils"
	"github.com/urfave/cli/v2"
)

func handleDownload(ctx *cli.Context) error {
	outputDir := ctx.String("output-dir")
	src := ctx.String("torrent")

	sessionId := [20]byte{}
	copy(sessionId[:], fmt.Appendf(nil, "-HA001-%s", utils.GenerateRandomString(13, "")))

	sesh := session.NewSession(sessionId)

	if err := sesh.AddTorrent(src, outputDir); err != nil {
		return err
	}

	sigC := make(chan os.Signal, 1)
	signal.Notify(sigC, syscall.SIGINT, syscall.SIGTERM)

	<-sigC
	sesh.Stop()

	return nil
}

func main() {
	app := &cli.App{
		Name:        "Hail",
		Usage:       "Download all your favourite torrents.",
		Description: "A basic BitTorrent client",
		Commands: []*cli.Command{
			{
				Name:   "download",
				Action: handleDownload,
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "output-dir",
						Aliases:  []string{"o"},
						Usage:    "destination directory where downloaded torrent files will be saved",
						Required: true,
					},
					&cli.StringFlag{
						Name:     "torrent",
						Aliases:  []string{"t"},
						Usage:    "torrent file of URL",
						Required: true,
					},
				},
			},
		},
	}

	if err := app.Run(os.Args); err != nil {
		log.Fatal(err)
	}
}
