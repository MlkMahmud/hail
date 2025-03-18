package utils

import (
	"math/rand"
	"time"
)

const (
	defaultCharacterSet = "abcdefghijklmnopqrstuvwxyz" +
		"ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
)

var (
	seededRand *rand.Rand = rand.New(
		rand.NewSource(time.Now().UnixNano()))
)

func GenerateRandomString(length int, charset string) string {
	if charset == "" {
		charset = defaultCharacterSet
	}

	byteArr := make([]byte, length)

	for i := range byteArr {
		byteArr[i] = charset[seededRand.Intn(len(charset))]
	}

	return string(byteArr)
}
