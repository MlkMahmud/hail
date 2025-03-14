package bencode

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

const (
	StartDelim     = 'i'
	ListStartDelim = 'l'
	EndDelim       = 'e'
)

func DecodeBencodedList(becondedString string) ([]any, error) {
	bencodedStringLen := len(becondedString)

	if bencodedStringLen == 0 {
		return nil, fmt.Errorf("bencoded list string is too short")
	}

	if becondedString[0] != ListStartDelim {
		return nil, fmt.Errorf("missing start delimeter '%c'", ListStartDelim)
	}

	decodedList := []any{}
	stringIndex := 1

	for stringIndex < bencodedStringLen && becondedString[stringIndex] != EndDelim {
		char := becondedString[stringIndex]

		switch {
		case char == 'i':
			{
				decodedInteger, err := DecodeBencodedInteger(becondedString[stringIndex:])

				if err != nil {
					return nil, err
				}

				decodedList = append(decodedList, decodedInteger)
				// The index of the next delimeter is calculated by adding
				// The number of characters in the decoded integer and '2', to account for the start and end delimter characters
				stringIndex += len(strconv.Itoa(decodedInteger)) + 2
				break
			}

		case unicode.IsDigit(rune(char)):
			{
				decodedString, err := DecodeBencodedString(becondedString[stringIndex:])

				if err != nil {
					return nil, err
				}
				decodedStringLen := len(decodedString)
				decodedList = append(decodedList, decodedString)

				// The index of the next delimeter is calculated by adding
				// the number of characters in the decimal before the ':' character
				// the length of the string
				// and '1', to account for the ':' character
				stringIndex += decodedStringLen + len(strconv.Itoa(decodedStringLen)) + 1
				break
			}

		default:
			{
				return nil, fmt.Errorf("unsupported delimeter '%c'", char)
			}
		}
	}

	if becondedString[stringIndex] != EndDelim {
		return nil, fmt.Errorf("missing end delimiter '%c'", EndDelim)
	}

	return decodedList, nil
}

func DecodeBencodedInteger(bencodedString string) (int, error) {
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

func DecodeBencodedString(bencodedString string) (string, error) {
	var firstColonIndex int
	bencodedStringLen := len(bencodedString)

	if bencodedStringLen == 0 {
		return "", fmt.Errorf("encoded string is empty")
	}

	if !unicode.IsDigit(rune(bencodedString[0])) {
		return "", fmt.Errorf("invalid string length %c''", bencodedString[0])
	}

	for i := range bencodedString {
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

	if (firstColonIndex + length) > bencodedStringLen {
		return "", fmt.Errorf("string length is invalid")
	}

	return bencodedString[firstColonIndex+1 : firstColonIndex+1+length], nil
}
