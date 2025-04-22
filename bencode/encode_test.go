package bencode_test

import (
	"fmt"
	"testing"

	"github.com/MlkMahmud/hail/bencode"
)

var inputs = []any{
	0,
	150,
	-100,
	"a",
	"a\"",
	"0123456789a",
	[]any{},
	[]any{1, 2},
	[]any{"abc", "def"},
	[]any{42, "abc"},
	map[string]any{},
	map[string]any{"cat": 1, "dog": 2},
	[]any{"spam", "eggs"},
	map[string]any{"cow": "moo", "spam": "eggs"},
	[]any{"foo", map[string]any{"d": 123}},
	map[string]any{"foo": []any{1, 2}, "bar": "world"},
	map[string]any{"announce": "udp://tracker.coppersurfer.tk:6969"},
	[]any{[]any{map[string]any{}, "foo"}, 5},
	map[string]any{"list": []any{"one", 2, "three", "five"}},
}

var expectedValues = []string{
	"i0e",
	"i150e",
	"i-100e",
	"1:a",
	"2:a\"",
	"11:0123456789a",
	"le",
	"li1ei2ee",
	"l3:abc3:defe",
	"li42e3:abce",
	"de",
	"d3:cati1e3:dogi2ee",
	"l4:spam4:eggse",
	"d3:cow3:moo4:spam4:eggse",
	"l3:food1:di123eee",
	"d3:bar5:world3:fooli1ei2eee",
	"d8:announce34:udp://tracker.coppersurfer.tk:6969e",
	"llde3:fooei5ee",
	"d4:listl3:onei2e5:three4:fiveee",
}

func TestEncoder(t *testing.T) {
	for index, value := range inputs {
		t.Run(fmt.Sprintf("encode  %v", value), func(t *testing.T) {
			encodedString, err := bencode.EncodeValue(value)
			expectedValue := expectedValues[index]

			if err != nil {
				t.Error(err)
			}

			if encodedString != expectedValue {
				t.Errorf("expected '%s' got '%s'", expectedValue, encodedString)
			}
		})
	}
}
