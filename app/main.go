package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"

	bencode "github.com/codecrafters-io/bittorrent-starter-go/app/bencode"
)

func decodeBencode(bencodedString string) (interface{}, error) {
	if len(bencodedString) == 0 {
		return nil, fmt.Errorf("bencoded string is empty")
	}

	decodedValue, _, err := bencode.DecodeBencodedValue(bencodedString)

	if err != nil {
		return nil, err
	}

	return decodedValue, nil
}

func main() {
	fmt.Fprintln(os.Stderr, "Logs from your program will appear here!")

	command := os.Args[1]

	if command == "decode" {
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
	} else {
		fmt.Println("Unknown command: " + command)
		os.Exit(1)
	}
}
