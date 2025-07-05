package bencode_test

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/MlkMahmud/hail/bencode"
)

func TestDecoder(t *testing.T) {
	inputs := map[string]any{
		"i0e":                         0,
		"i150e":                       150,
		"i-100e":                      -100,
		"1:a":                         "a",
		"2:a\"":                       "a\"",
		"11:0123456789a":              "0123456789a",
		"le":                          []any{},
		"li1ei2ee":                    []any{1, 2},
		"l3:abc3:defe":                []any{"abc", "def"},
		"li42e3:abce":                 []any{42, "abc"},
		"de":                          map[string]any{},
		"d3:cati1e3:dogi2ee":          map[string]any{"cat": 1, "dog": 2},
		"l4:spam4:eggse":              []any{"spam", "eggs"},
		"d3:cow3:moo4:spam4:eggse":    map[string]any{"cow": "moo", "spam": "eggs"},
		"l3:food1:di123eee":           []any{"foo", map[string]any{"d": 123}},
		"d3:fooli1ei2ee3:bar5:worlde": map[string]any{"foo": []any{1, 2}, "bar": "world"},
		"d8:announce34:udp://tracker.coppersurfer.tk:6969e": map[string]any{"announce": "udp://tracker.coppersurfer.tk:6969"},
		"llde3:fooei5eee":                 []any{[]any{map[string]any{}, "foo"}, 5},
		"d4:listl3:onei2e5:three4:fiveee": map[string]any{"list": []any{"one", 2, "three", "five"}},
	}

	for bencodedString, expectedValue := range inputs {
		t.Run(fmt.Sprintf("decode %s", bencodedString), func(t *testing.T) {

			decodedValue, _, err := bencode.DecodeValue([]byte(bencodedString))

			if err != nil {
				t.Error(err)
			}

			if !reflect.DeepEqual(expectedValue, decodedValue) {
				t.Errorf("Expected %v got %v\n", expectedValue, decodedValue)
			}
		})
	}
}
