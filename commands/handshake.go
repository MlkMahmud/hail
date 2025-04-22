package commands

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/MlkMahmud/hail/torrent"
	"github.com/urfave/cli/v2"
)

func HandleHandshakeCommand(ctx *cli.Context) error {
	src := ctx.Args().Get(0)
	peerAddress := ctx.Args().Get(1)

	trrnt, err := torrent.NewTorrent(src)

	if err != nil {
		return err
	}

	var peer torrent.Peer

	if peerAddress == "" {
		fmt.Println("fetching peers...")
		peers, err := trrnt.GetPeers()

		if err != nil {
			return err
		}

		fmt.Println(peers)

		peer = peers[0]
	} else {
		addressParts := strings.Split(peerAddress, ":")
		host := addressParts[0]
		portNum, err := strconv.ParseUint(addressParts[1], 10, 16)

		if err != nil {
			return err
		}

		port := uint16(portNum)
		peer = torrent.Peer{InfoHash: trrnt.InfoHash, IpAddress: host, Port: port}
	}

	peerConnection := torrent.NewPeerConnection(torrent.PeerConnectionConfig{Peer: peer})

	defer peerConnection.Close()

	if err := peerConnection.InitConnection(); err != nil {
		return err
	}

	fmt.Printf("Peer ID: %x\n", peerConnection.PeerId)

	if peerConnection.SupportsExtensions {
		fmt.Printf("Peer Metadata Extension ID: %d\n", peerConnection.PeerExtensions[torrent.Metadata])
	}

	return nil
}
