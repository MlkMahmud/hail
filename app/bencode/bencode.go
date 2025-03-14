package bencode

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

const (
	DictStartDelim    = 'd'
	IntegerStartDelim = 'i'
	ListStartDelim    = 'l'
	EndDelim          = 'e'
)

func DecodeBencodedDict(becondedString string) (map[string]any, int, error) {
	bencodedStringLen := len(becondedString)

	if bencodedStringLen == 0 {
		return nil, 0, fmt.Errorf("bencoded list string is too short")
	}

	if becondedString[0] != DictStartDelim {
		return nil, 0, fmt.Errorf("missing start delimeter '%c'", DictStartDelim)
	}

	strIndex := 1

	for strIndex < bencodedStringLen && becondedString[strIndex] != EndDelim {
		key, valueStartDelimIndex, err := DecodeBencodedString(becondedString[strIndex:])

		if err != nil {
			return nil, 0, err
		}

		strIndex += valueStartDelimIndex
		value, nextDelimIndex, err := deco
	}

	return nil, 0, fmt.Errorf("")
}

func DecodeBencodedList(becondedString string) ([]any, int, error) {
	bencodedStringLen := len(becondedString)

	if bencodedStringLen == 0 {
		return nil, 0, fmt.Errorf("bencoded list string is too short")
	}

	if becondedString[0] != ListStartDelim {
		return nil, 0, fmt.Errorf("missing start delimeter '%c'", ListStartDelim)
	}

	decodedList := []any{}
	stringIndex := 1

	for stringIndex < bencodedStringLen && becondedString[stringIndex] != EndDelim {
		char := becondedString[stringIndex]

		switch {
		case char == IntegerStartDelim:
			{
				decodedInteger, nextDelimIndex, err := DecodeBencodedInteger(becondedString[stringIndex:])

				if err != nil {
					return nil, 0, err
				}

				decodedList = append(decodedList, decodedInteger)
				stringIndex += nextDelimIndex
				break
			}

		case char == ListStartDelim:
			{
				decodedNestedList, nextDelimIndex, err := DecodeBencodedList(becondedString[stringIndex:])

				if err != nil {
					return nil, 0, err
				}

				decodedList = append(decodedList, decodedNestedList)
				stringIndex += nextDelimIndex
				break
			}

		case unicode.IsDigit(rune(char)):
			{
				decodedString, nextDelimIndex, err := DecodeBencodedString(becondedString[stringIndex:])

				if err != nil {
					return nil, 0, err
				}

				decodedList = append(decodedList, decodedString)
				stringIndex += nextDelimIndex
				break
			}

		default:
			{
				return nil, 0, fmt.Errorf("unsupported delimeter '%c'", char)
			}
		}
	}

	if becondedString[stringIndex] != EndDelim {
		return nil, 0, fmt.Errorf("missing end delimiter '%c'", EndDelim)
	}

	return decodedList, stringIndex + 1, nil
}

func DecodeBencodedInteger(bencodedString string) (int, int, error) {
	bencodedStringLen := len(bencodedString)

	if bencodedStringLen < 3 {
		return 0, 0, fmt.Errorf("bencoded integer string too short")
	}

	if bencodedString[0] != IntegerStartDelim {
		return 0, 0, fmt.Errorf("missing start delimeter '%c'", IntegerStartDelim)
	}

	startIndex := 1
	endIndex := startIndex

	if strings.HasPrefix(bencodedString[startIndex:], "-0") || strings.HasPrefix(bencodedString[startIndex:], "0") && (startIndex+1 < bencodedStringLen && bencodedString[startIndex+1] != EndDelim) {
		return 0, 0, fmt.Errorf("invalid leading zero")
	}

	for ; endIndex < bencodedStringLen && bencodedString[endIndex] != EndDelim; endIndex++ {
	}

	if endIndex >= bencodedStringLen || bencodedString[endIndex] != EndDelim {
		return 0, 0, fmt.Errorf("missing end delimiter '%c'", EndDelim)
	}

	result, err := strconv.Atoi(string(bencodedString[startIndex:endIndex]))

	if err != nil {
		return 0, 0, err
	}

	return result, endIndex + 1, nil
}

func DecodeBencodedString(bencodedString string) (string, int, error) {
	var firstColonIndex int
	bencodedStringLen := len(bencodedString)

	if bencodedStringLen == 0 {
		return "", 0, fmt.Errorf("encoded string is empty")
	}

	if !unicode.IsDigit(rune(bencodedString[0])) {
		return "", 0, fmt.Errorf("invalid string length %c''", bencodedString[0])
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

	if unicode.IsDigit(rune(bencodedString[0])) {
		decodedString, nextDelimIndex, err := DecodeBencodedString(bencodedString)

		if err != nil {
			return "", 0, err
		}

		return decodedString, nextDelimIndex, nil
	} else if bencodedString[0] == 'i' {
		decodedInterger, nextDelimIndex, err := DecodeBencodedInteger(bencodedString)

		if err != nil {
			return 0, 0, err
		}

		return decodedInterger, nextDelimIndex, nil
	} else if bencodedString[0] == 'l' {
		decodedList, nextDelimIndex, err := DecodeBencodedList(bencodedString)

		if err != nil {
			return nil, 0, err
		}

		return decodedList, nextDelimIndex, nil
	} else {
		return "", 0, fmt.Errorf("Only strings are supported at the moment")
	}
}
