package utils

import (
	"errors"
	"io"
	"math/rand"
	"net"
	"os"
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

func CheckIfFileExists(filepath string) bool {
	_, err := os.Stat(filepath)

	return !errors.Is(err, os.ErrNotExist)
}

func ConnReadFull(conn net.Conn, buffer []byte) (int, error) {
	bufferSize := len(buffer)
	readStartIndex := 0

	if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		return readStartIndex, err
	}

	for readStartIndex < bufferSize {
		bytesRead, err := conn.Read(buffer[readStartIndex:])

		if err == io.EOF {
			return readStartIndex, io.ErrUnexpectedEOF
		}

		if err != nil {
			return readStartIndex, err
		}

		readStartIndex += bytesRead
	}

	return readStartIndex, nil
}

func ConnWriteFull(conn net.Conn, buffer []byte) (int, error) {
	bufferSize := len(buffer)
	writeStartIndex := 0

	if err := conn.SetWriteDeadline(time.Now().Add(3 * time.Second)); err != nil {
		return writeStartIndex, err
	}

	for writeStartIndex < bufferSize {
		bytesWritten, err := conn.Write(buffer[writeStartIndex:])

		if err != nil {
			return writeStartIndex, err
		}

		writeStartIndex += bytesWritten
	}

	return writeStartIndex, nil
}

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
