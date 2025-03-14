package bencode

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

const (
	dictStartDelim    = 'd'
	integerStartDelim = 'i'
	listStartDelim    = 'l'
	endDelim          = 'e'
)

func decodeBencodedDict(becondedString string) (map[string]any, int, error) {
	bencodedStringLen := len(becondedString)

	if bencodedStringLen == 0 {
		return nil, 0, fmt.Errorf("bencoded list string is too short")
	}

	if becondedString[0] != dictStartDelim {
		return nil, 0, fmt.Errorf("missing start delimeter '%c'", dictStartDelim)
	}

	strIndex := 1
	decodedDict := map[string]any{}

	for strIndex < bencodedStringLen && becondedString[strIndex] != endDelim {
		key, valueStartDelimIndex, err := decodeBencodedString(becondedString[strIndex:])

		if err != nil {
			return nil, 0, err
		}

		strIndex += valueStartDelimIndex
		value, nextDelimIndex, err := DecodeBencodedValue(becondedString[strIndex:])

		if err != nil {
			return nil, 0, err
		}

		strIndex += nextDelimIndex
		decodedDict[key] = value
	}

	return decodedDict, strIndex + 1, nil
}

func decodeBencodedList(becondedString string) ([]any, int, error) {
	bencodedStringLen := len(becondedString)

	if bencodedStringLen == 0 {
		return nil, 0, fmt.Errorf("bencoded list string is too short")
	}

	if becondedString[0] != listStartDelim {
		return nil, 0, fmt.Errorf("missing start delimeter '%c'", listStartDelim)
	}

	decodedList := []any{}
	stringIndex := 1

	for stringIndex < bencodedStringLen && becondedString[stringIndex] != endDelim {
		decodedValue, nextDelimIndex, err := DecodeBencodedValue(becondedString[stringIndex:])

		if err != nil {
			return nil, 0, err
		}

		decodedList = append(decodedList, decodedValue)
		stringIndex += nextDelimIndex
	}

	if becondedString[stringIndex] != endDelim {
		return nil, 0, fmt.Errorf("missing end delimiter '%c'", endDelim)
	}

	return decodedList, stringIndex + 1, nil
}

func decodeBencodedInteger(bencodedString string) (int, int, error) {
	bencodedStringLen := len(bencodedString)

	if bencodedStringLen < 3 {
		return 0, 0, fmt.Errorf("bencoded integer string too short")
	}

	if bencodedString[0] != integerStartDelim {
		return 0, 0, fmt.Errorf("missing start delimeter '%c'", integerStartDelim)
	}

	startIndex := 1
	endIndex := startIndex

	if strings.HasPrefix(bencodedString[startIndex:], "-0") || strings.HasPrefix(bencodedString[startIndex:], "0") && (startIndex+1 < bencodedStringLen && bencodedString[startIndex+1] != endDelim) {
		return 0, 0, fmt.Errorf("invalid leading zero")
	}

	for ; endIndex < bencodedStringLen && bencodedString[endIndex] != endDelim; endIndex++ {
	}

	if endIndex >= bencodedStringLen || bencodedString[endIndex] != endDelim {
		return 0, 0, fmt.Errorf("missing end delimiter '%c'", endDelim)
	}

	result, err := strconv.Atoi(string(bencodedString[startIndex:endIndex]))

	if err != nil {
		return 0, 0, err
	}

	return result, endIndex + 1, nil
}

func decodeBencodedString(bencodedString string) (string, int, error) {
	var firstColonIndex int
	bencodedStringLen := len(bencodedString)

	if bencodedStringLen == 0 {
		return "", 0, fmt.Errorf("encoded string is empty")
	}

	if !unicode.IsDigit(rune(bencodedString[0])) {
		return "", 0, fmt.Errorf("invalid string length '%c'", bencodedString[0])
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
		return "", 0, err
	}

	if (firstColonIndex + length) > bencodedStringLen {
		return "", 0, fmt.Errorf("string length is invalid")
	}

	endIndex := firstColonIndex + 1 + length
	return bencodedString[firstColonIndex+1 : endIndex], endIndex, nil
}

func DecodeBencodedValue(bencodedString string) (any, int, error) {
	if len(bencodedString) == 0 {
		return nil, 0, fmt.Errorf("bencoded string is empty")
	}

	char := bencodedString[0]

	switch {
	case unicode.IsDigit(rune(char)):
		{
			decodedString, nextDelimIndex, err := decodeBencodedString(bencodedString)

			if err != nil {
				return "", 0, err
			}

			return decodedString, nextDelimIndex, nil
		}

	case char == dictStartDelim:
		{
			decodedDict, nextDelimIndex, err := decodeBencodedDict(bencodedString)

			if err != nil {
				return nil, 0, err
			}

			return decodedDict, nextDelimIndex, nil
		}

	case char == integerStartDelim:
		{
			decodedInterger, nextDelimIndex, err := decodeBencodedInteger(bencodedString)

			if err != nil {
				return 0, 0, err
			}

			return decodedInterger, nextDelimIndex, nil
		}

	case char == listStartDelim:
		{
			decodedList, nextDelimIndex, err := decodeBencodedList(bencodedString)

			if err != nil {
				return nil, 0, err
			}

			return decodedList, nextDelimIndex, nil
		}

	default:
		{
			return nil, 0, fmt.Errorf("unsupported delimeter '%c'", char)
		}
	}
}
