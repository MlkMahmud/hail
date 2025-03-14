package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"unicode"

	bencode "github.com/codecrafters-io/bittorrent-starter-go/app/bencode"
)

func decodeBencode(bencodedString string) (interface{}, error) {
	if len(bencodedString) == 0 {
		return nil, fmt.Errorf("bencoded string is empty")
	}

	if unicode.IsDigit(rune(bencodedString[0])) {
		decodedString, _, err := bencode.DecodeBencodedString(bencodedString)

		if err != nil {
			return "", err
		}

		return decodedString, nil
	} else if bencodedString[0] == 'i' {
		decodedInterger, _, err := bencode.DecodeBencodedInteger(bencodedString)

		if err != nil {
			return 0, err
		}

		return decodedInterger, nil
	} else if bencodedString[0] == 'l' {
		decodedList, _, err := bencode.DecodeBencodedList(bencodedString)

		if err != nil {
			return nil, err
		}

		return decodedList, nil
	} else {
		return "", fmt.Errorf("Only strings are supported at the moment")
	}
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
