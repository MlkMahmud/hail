package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/codecrafters-io/bittorrent-starter-go/app/bencode"
	"github.com/codecrafters-io/bittorrent-starter-go/app/torrent"
)

func main() {
	fmt.Fprintln(os.Stderr, "Logs from your program will appear here!")

	command := os.Args[1]

	switch command {
	case "decode":
		{
			bencodedValue := os.Args[2]
			decoded, _ , err := bencode.DecodeValue([]byte(bencodedValue))

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

			fileContent, err := os.ReadFile(torrentFilePath)

			if err != nil {
				log.Fatal(err)
			}

			decodedValue, _, err := bencode.DecodeValue(fileContent)

			if err != nil {
				log.Fatal(err)
			}

			trrnt, torrentErr := torrent.NewTorrent(decodedValue)

			if torrentErr != nil {
				log.Fatal(torrentErr)
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

			handshakeResp, err := trrnt.ConnectToPeer(torrent.Peer{IpAddress: host, Port: port})

			if err != nil {
				log.Fatal(err)
			}

			fmt.Printf("Peer ID: %x\n", handshakeResp[48:])
		}

	case "info":
		{
			torrentFilePath := os.Args[2]

			fileContent, err := os.ReadFile(torrentFilePath)

			if err != nil {
				log.Fatal(err)
			}

			decodedValue, _, err := bencode.DecodeValue(fileContent)

			if err != nil {
				log.Fatal(err)
			}

			trrnt, torrentErr := torrent.NewTorrent(decodedValue)

			if torrentErr != nil {
				log.Fatal(torrentErr)
			}

			fmt.Printf("Tracker URL: %s\n", trrnt.TrackerUrl)
			fmt.Printf("Length: %d\n", trrnt.Info.Length)
			fmt.Printf("Info Hash: %x\n", trrnt.InfoHash)
			fmt.Printf("Piece Length: %d\n", trrnt.Info.PieceLength)
			fmt.Println("Piece Hashes:")
			for _, value := range trrnt.Info.PieceHashes {
				fmt.Printf("%x\n", value)
			}

			return
		}

	case "peers":
		{
			torrentFilePath := os.Args[2]

			fileContent, err := os.ReadFile(torrentFilePath)

			if err != nil {
				log.Fatal(err)
			}

			decodedValue, _, err := bencode.DecodeValue(fileContent)

			if err != nil {
				log.Fatal(err)
			}

			trrnt, torrentErr := torrent.NewTorrent(decodedValue)

			if torrentErr != nil {
				log.Fatal(torrentErr)
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

	default:
		{
			fmt.Println("Unknown command: " + command)
			os.Exit(1)
		}
	}
}
