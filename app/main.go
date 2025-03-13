package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"unicode"
	// bencode "github.com/jackpal/bencode-go" // Available if you need it!
)

// Ensures gofmt doesn't remove the "os" encoding/json import (feel free to remove this!)
var _ = json.Marshal

func decodeBencodedInteger(bencodedString string) (int, error) {
	const StartDelim, EndDelim = 'i', 'e'
	bencodedStringLen := len(bencodedString)

	if bencodedStringLen < 3 {
		return 0, fmt.Errorf("bencoded integer string too short")
	}

	if bencodedString[0] != StartDelim {
		return 0, fmt.Errorf("missing start delimeter '%c'", StartDelim)
	}

	startIndex := 1
	endIndex := startIndex

	if strings.HasPrefix(bencodedString[startIndex:], "-0") || strings.HasPrefix(bencodedString[startIndex:], "0") && (startIndex+1 < bencodedStringLen && bencodedString[startIndex+1] != EndDelim) {
		return 0, fmt.Errorf("invalid leading zero")
	}

	if bencodedString[startIndex] == '-' {
		endIndex++
	}

	for ; endIndex < bencodedStringLen && bencodedString[endIndex] != EndDelim; endIndex++ {
	}

	if endIndex >= bencodedStringLen || bencodedString[endIndex] != EndDelim {
		return 0, fmt.Errorf("missing end delimiter '%c'", EndDelim)
	}

	result, err := strconv.Atoi(string(bencodedString[startIndex:endIndex]))

	if err != nil {
		return 0, err
	}

	return result, nil
}

// Example:
// - 5:hello -> hello
// - 10:hello12345 -> hello12345
func decodeBencode(bencodedString string) (interface{}, error) {
	if unicode.IsDigit(rune(bencodedString[0])) {
		var firstColonIndex int

		for i := 0; i < len(bencodedString); i++ {
			if bencodedString[i] == ':' {
				firstColonIndex = i
				break
			}
		}

		lengthStr := bencodedString[:firstColonIndex]

		length, err := strconv.Atoi(lengthStr)
		if err != nil {
			return "", err
		}

		return bencodedString[firstColonIndex+1 : firstColonIndex+1+length], nil
	} else if bencodedString[0] == 'i' {
		decodedInterger, err := decodeBencodedInteger(bencodedString)

		if err != nil {
			return "", err
		}

		return decodedInterger, nil
	} else {
		return "", fmt.Errorf("Only strings are supported at the moment")
	}
}

func main() {
	// You can use print statements as follows for debugging, they'll be visible when running tests.
	fmt.Fprintln(os.Stderr, "Logs from your program will appear here!")

	command := os.Args[1]

	if command == "decode" {
		// Uncomment this block to pass the first stage
		//
		bencodedValue := os.Args[2]
		decoded, err := decodeBencode(bencodedValue)

		if err != nil {
			fmt.Println(err)
			return
		}

		if bencodedValue[0] == 'i' {
			fmt.Printf("%d\n", decoded)
			return
		}

		jsonOutput, _ := json.Marshal(decoded)
		fmt.Println(string(jsonOutput))
	} else {
		fmt.Println("Unknown command: " + command)
		os.Exit(1)
	}
}
