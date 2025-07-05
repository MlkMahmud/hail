package main

import (
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/MlkMahmud/hail/internal/session"
	"github.com/urfave/cli/v2"
)

var (
	logger *slog.Logger
)

var app = &cli.App{
	Name:        "Hail",
	Usage:       "Download all your favourite torrents.",
	Description: "A basic BitTorrent client",
	Before: func(ctx *cli.Context) error {
		logLevel := slog.LevelError

		if ctx.Bool("d") {
			logLevel = slog.LevelDebug
		}

		logger = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel}))
		return nil
	},
	Commands: []*cli.Command{
		{
			Name: "download",
			Action: func(ctx *cli.Context) error {
				outputDir := ctx.String("output-dir")
				src := ctx.String("torrent")

				sesh := session.NewSession(session.SessionOpts{Logger: logger})

				if err := sesh.AddTorrent(src, outputDir); err != nil {
					return err
				}

				sesh.StartAllTorrents()

				sigC := make(chan os.Signal, 1)
				signal.Notify(sigC, syscall.SIGINT, syscall.SIGTERM)

				<-sigC

				sesh.StopAllTorrents()

				return nil
			},
			Usage: "downloads a single torrent from the user-provided source",
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:    "output-dir",
					Aliases: []string{"o"},
					Usage:   "destination directory where downloaded torrent files will be saved",
					Value:   ".",
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
	Flags: []cli.Flag{
		&cli.BoolFlag{
			Name:    "debug",
			Aliases: []string{"d"},
			Usage:   "enable debug logging output for troubleshooting and development",
		},
	},
}

func main() {
	if err := app.Run(os.Args); err != nil {
		log.Fatal(err)
	}
}
