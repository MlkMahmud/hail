package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/codecrafters-io/bittorrent-starter-go/app/bencode"
	"github.com/codecrafters-io/bittorrent-starter-go/app/torrent"
)

func decodeBencode(bencodedString string) (interface{}, error) {
	if len(bencodedString) == 0 {
		return nil, fmt.Errorf("bencoded string is empty")
	}

	decodedValue, _, err := bencode.DecodeValue(bencodedString)

	if err != nil {
		return nil, err
	}

	return decodedValue, nil
}

func main() {
	fmt.Fprintln(os.Stderr, "Logs from your program will appear here!")

	command := os.Args[1]

	switch command {
	case "decode":
		{
			bencodedValue := os.Args[2]
			decoded, err := decodeBencode(bencodedValue)

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

	case "info":
		{
			torrentFilePath := os.Args[2]

			fileContent, err := os.ReadFile(torrentFilePath)

			if err != nil {
				log.Fatal(err)
			}

			decodedValue, _, err := bencode.DecodeValue(string(fileContent))

			if err != nil {
				log.Fatal(err)
			}

			torrentStruct, torrentErr := torrent.NewTorrent(decodedValue)

			if torrentErr != nil {
				log.Fatal(torrentErr)
			}

			fmt.Printf("Tracker URL: %s\n", torrentStruct.TrackerUrl)
			fmt.Printf("Length: %d\n", torrentStruct.Info.Length)
			fmt.Printf("Info Hash: %x\n", torrentStruct.InfoHash)
			fmt.Printf("Piece Length: %d\n", torrentStruct.Info.PieceLength)
			fmt.Println("Piece Hashes:")
			for _, value := range torrentStruct.Info.PieceHashes {
				fmt.Printf("%x\n", value)
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
