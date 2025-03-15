package bencode

import (
	"fmt"
	"strconv"
	"strings"
)

func encodeDict(dict map[string]any) (string, error) {
	entries := []string{}

	for key, value := range dict {
		bencodedKey := encodeString(key)
		bencodedValue, err := EncodeValue(value)

		if err != nil {
			return "", err
		}

		entries = append(entries, []string{bencodedKey, bencodedValue}...)
	}

	return fmt.Sprintf("%c%s%c", dictStartDelim, strings.Join(entries, ""), endDelim), nil
}

func encodeInteger(num int) string {
	numStr := strconv.Itoa(num)

	return fmt.Sprintf("%c%s%c", integerStartDelim, numStr, endDelim)
}

func encodeList(list []any) (string, error) {
	items := []string{}

	for _, item := range list {
		bencodedValue, err := EncodeValue(item)

		if err != nil {
			return "", err
		}

		items = append(items, bencodedValue)
	}

	return fmt.Sprintf("%c%s%c", listStartDelim, strings.Join(items, ""), endDelim), nil

}

func encodeString(str string) string {
	strLen := len(str)

	if strLen == 0 {
		return "0:"
	}

	return fmt.Sprintf("%d:%s", strLen, str)
}

func EncodeValue(value any) (string, error) {
	switch v := value.(type) {
	case map[string]any:
		{
			bencodedDict, err := encodeDict(v)

			if err != nil {
				return "", err
			}

			return bencodedDict, nil
		}

	case []any:
		{
			bencodedList, err := encodeList(v)

			if err != nil {
				return "", err
			}

			return bencodedList, nil
		}

	case string:
		{
			bencodedString := encodeString(v)
			return bencodedString, nil
		}

	case int:
		{
			bencodedInteger := encodeInteger(v)
			return bencodedInteger, nil
		}

	default:
		{
			return "", fmt.Errorf("unsupported type %T", value)
		}
	}
}
