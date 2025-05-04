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

type RetryOptions[T any] struct {
	Delay      time.Duration
	MaxAttemps int
	Operation  func() (T, error)
}

func FileExists(filepath string) bool {
	_, err := os.Stat(filepath)

	return !errors.Is(err, os.ErrNotExist)
}

func ConnReadFull(conn net.Conn, buffer []byte, wait time.Duration) (int, error) {
	bufferSize := len(buffer)
	readStartIndex := 0

	duration := wait

	if duration == 0 {
		duration = 5 * time.Second
	}

	if err := conn.SetReadDeadline(time.Now().Add(duration)); err != nil {
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

func ConnWriteFull(conn net.Conn, buffer []byte, wait time.Duration) (int, error) {
	bufferSize := len(buffer)
	writeStartIndex := 0

	duration := wait

	if duration == 0 {
		duration = 5 * time.Second
	}

	if err := conn.SetWriteDeadline(time.Now().Add(duration)); err != nil {
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

func Retry[T any](options RetryOptions[T]) (T, error) {
	var res T
	var err error

	for range options.MaxAttemps {
		res, err = options.Operation()

		if err == nil {
			break
		}

		time.Sleep(options.Delay)
	}

	return res, err
}
