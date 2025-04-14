package commands

import (
	"fmt"

	"github.com/codecrafters-io/bittorrent-starter-go/app/torrent"
	"github.com/urfave/cli/v2"
)

func HandlePeersCommand(ctx *cli.Context) error {
	src := ctx.Args().First()

	trrnt, err := torrent.NewTorrent(src)

	if err != nil {
		return err
	}

	peers, err := trrnt.GetPeers()

	if err != nil {
		return err
	}

	for _, peer := range peers {
		fmt.Printf("%s:%d\n", peer.IpAddress, peer.Port)
	}

	return nil
}
