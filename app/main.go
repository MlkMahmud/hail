package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/codecrafters-io/bittorrent-starter-go/app/bencode"
	"github.com/codecrafters-io/bittorrent-starter-go/app/client"
	"github.com/codecrafters-io/bittorrent-starter-go/app/torrent"
)

func main() {
	fmt.Fprintln(os.Stderr, "Logs from your program will appear here!")

	command := os.Args[1]

	switch command {
	case "decode":
		{
			bencodedValue := os.Args[2]
			decoded, _, err := bencode.DecodeValue([]byte(bencodedValue))

			if err != nil {
				log.Fatal(err)
				return
			}

			jsonString, err := json.Marshal(decoded)

			if err != nil {
				log.Fatal(err)
			}

			fmt.Println(string(jsonString))
			return
		}

	case "handshake":
		{
			torrentFilePath := os.Args[2]
			trrnt, err := torrent.NewTorrent(torrentFilePath)

			if err != nil {
				log.Fatal(err)
			}

			if _, err := trrnt.GetPeers(); err != nil {
				log.Fatal(err)
			}

			peerAddress := os.Args[3]
			addressParts := strings.Split(peerAddress, ":")
			host := addressParts[0]

			portNum, err := strconv.ParseUint(addressParts[1], 10, 16)

			if err != nil {
				log.Fatal(err)
			}

			port := uint16(portNum)
			peer := torrent.Peer{IpAddress: host, Port: port}

			conn, err := net.DialTimeout("tcp", net.JoinHostPort(peer.IpAddress, strconv.Itoa(int(peer.Port))), 3*time.Second)

			if err != nil {
				log.Fatal(err)
			}

			defer conn.Close()

			handshakeResp, err := client.EstablishHandshake(conn, trrnt.InfoHash)

			if err != nil {
				log.Fatal(err)
			}

			fmt.Printf("Peer ID: %x\n", handshakeResp[48:])
		}

	case "info":
		{
			torrentFilePath := os.Args[2]
			trrnt, err := torrent.NewTorrent(torrentFilePath)

			if err != nil {
				log.Fatal(err)
			}

			fmt.Printf("Tracker URL: %s\n", trrnt.TrackerUrl)
			fmt.Printf("Length: %d\n", trrnt.Info.Length)
			fmt.Printf("Info Hash: %x\n", trrnt.InfoHash)
			fmt.Printf("Piece Length: %d\n", trrnt.Info.Pieces[0].Length)
			fmt.Println("Piece Hashes:")
			for _, piece := range trrnt.Info.Pieces {
				fmt.Printf("%x\n", piece.Hash)
			}

			return
		}

	case "peers":
		{
			torrentFilePath := os.Args[2]
			trrnt, err := torrent.NewTorrent(torrentFilePath)

			if err != nil {
				log.Fatal(err)
			}

			peers, err := trrnt.GetPeers()

			if err != nil {
				log.Fatal(err)
			}

			for _, peer := range peers {
				fmt.Printf("%s:%d\n", peer.IpAddress, peer.Port)
			}

			return
		}

	case "download_piece":
		{
			dest := os.Args[3]
			torrentFilePath := os.Args[4]
			pieceIndex, err := strconv.Atoi(os.Args[5])

			if err != nil {
				log.Fatal(err)
			}

			trrnt, err := torrent.NewTorrent(torrentFilePath)

			if err != nil {
				log.Fatal(err)
			}

			peers, err := trrnt.GetPeers()

			if err != nil {
				log.Fatal(err)
			}

			data, err := client.DownloadPiece(trrnt.Info.Pieces[pieceIndex], peers[0], trrnt.InfoHash)

			if err != nil {
				log.Fatal(err)
			}

			if err := os.WriteFile(dest, data, 0644); err != nil {
				log.Fatal(err)
			}

			return
		}

	default:
		{
			fmt.Println("Unknown command: " + command)
			os.Exit(1)
		}
	}
}
