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

func decodeDict(bencodedString []byte) (map[string]any, int, error) {
	bencodedStringLen := len(bencodedString)

	if bencodedStringLen == 0 {
		return nil, 0, fmt.Errorf("bencoded list string is too short")
	}

	if bencodedString[0] != dictStartDelim {
		return nil, 0, fmt.Errorf("missing start delimeter '%c'", dictStartDelim)
	}

	strIndex := 1
	decodedDict := map[string]any{}

	for strIndex < bencodedStringLen && bencodedString[strIndex] != endDelim {
		key, valueStartDelimIndex, err := decodeString(bencodedString[strIndex:])

		if err != nil {
			return nil, 0, err
		}

		strIndex += valueStartDelimIndex
		value, nextDelimIndex, err := DecodeValue(bencodedString[strIndex:])

		if err != nil {
			return nil, 0, err
		}

		strIndex += nextDelimIndex
		decodedDict[key] = value
	}

	return decodedDict, strIndex + 1, nil
}

func decodeList(bencodedString []byte) ([]any, int, error) {
	bencodedStringLen := len(bencodedString)

	if bencodedStringLen == 0 {
		return nil, 0, fmt.Errorf("bencoded list string is too short")
	}

	if bencodedString[0] != listStartDelim {
		return nil, 0, fmt.Errorf("missing start delimeter '%c'", listStartDelim)
	}

	decodedList := []any{}
	stringIndex := 1

	for stringIndex < bencodedStringLen && bencodedString[stringIndex] != endDelim {
		decodedValue, nextDelimIndex, err := DecodeValue(bencodedString[stringIndex:])

		if err != nil {
			return nil, 0, err
		}

		decodedList = append(decodedList, decodedValue)
		stringIndex += nextDelimIndex
	}

	if stringIndex >= bencodedStringLen {
		return nil, 0, fmt.Errorf("unexpected end of input")
	}

	if bencodedString[stringIndex] != endDelim {
		return nil, 0, fmt.Errorf("missing end delimiter '%c'", endDelim)
	}

	return decodedList, stringIndex + 1, nil
}

func decodeInteger(bencodedString []byte) (int, int, error) {
	bencodedStringLen := len(bencodedString)

	if bencodedStringLen < 3 {
		return 0, 0, fmt.Errorf("bencoded integer string too short")
	}

	if bencodedString[0] != integerStartDelim {
		return 0, 0, fmt.Errorf("missing start delimeter '%c'", integerStartDelim)
	}

	startIndex := 1
	endIndex := startIndex

	if strings.HasPrefix(string(bencodedString[startIndex:]), "-0") || strings.HasPrefix(string(bencodedString[startIndex:]), "0") && (startIndex+1 < bencodedStringLen && bencodedString[startIndex+1] != endDelim) {
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

func decodeString(bencodedString []byte) (string, int, error) {
	firstColonIndex := -1
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

	if firstColonIndex == -1 {
		return "", 0, fmt.Errorf("missing colon character in encoded string")
	}

	lengthStr := bencodedString[:firstColonIndex]
	length, err := strconv.Atoi(string(lengthStr))

	if err != nil {
		return "", 0, err
	}

	if (firstColonIndex + length) > bencodedStringLen {
		return "", 0, fmt.Errorf("string length is invalid")
	}

	endIndex := firstColonIndex + length + 1

	if endIndex > bencodedStringLen {
		return "", 0, fmt.Errorf("unexpected end of input")
	}

	return string(bencodedString[firstColonIndex+1 : endIndex]), endIndex, nil
}

func DecodeValue(bencodedString []byte) (any, int, error) {
	if len(bencodedString) == 0 {
		return nil, 0, fmt.Errorf("bencoded string is empty")
	}

	char := bencodedString[0]

	switch {
	case unicode.IsDigit(rune(char)):
		{
			decodedString, nextDelimIndex, err := decodeString(bencodedString)

			if err != nil {
				return "", 0, err
			}

			return decodedString, nextDelimIndex, nil
		}

	case char == dictStartDelim:
		{
			decodedDict, nextDelimIndex, err := decodeDict(bencodedString)

			if err != nil {
				return nil, 0, err
			}

			return decodedDict, nextDelimIndex, nil
		}

	case char == integerStartDelim:
		{
			decodedInterger, nextDelimIndex, err := decodeInteger(bencodedString)

			if err != nil {
				return 0, 0, err
			}

			return decodedInterger, nextDelimIndex, nil
		}

	case char == listStartDelim:
		{
			decodedList, nextDelimIndex, err := decodeList(bencodedString)

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
