package utils

import (
	"errors"
	"io"
	"net"
	"os"
	"time"
)

type RetryOptions[T any] struct {
	Delay       time.Duration
	MaxAttempts int
	Operation   func() (T, error)
}

func FileExists(filepath string) bool {
	_, err := os.Stat(filepath)

	return !errors.Is(err, os.ErrNotExist)
}

// Reads exactly len(buffer) bytes from the provided net.Conn into buffer.
// If a non-zero deadline is provided, it sets the read deadline on the connection before reading.
// If the deadline is zero, no deadline is set and the function may block indefinitely
// until all bytes are read or an error occurs.
// Returns the number of bytes read and any error encountered.
func ConnReadFull(conn net.Conn, buffer []byte, deadline time.Time) (int, error) {
	if !deadline.IsZero() {
		if err := conn.SetReadDeadline(deadline); err != nil {
			return 0, err
		}

		defer conn.SetReadDeadline(time.Time{})
	}

	return io.ReadFull(conn, buffer)
}

// Writes exactly len(buffer) bytes from the provided buffer to the given net.Conn.
// If a non-zero deadline is provided, it sets the write deadline on the connection before writing.
// If the deadline is zero, no deadline is set and the function may block indefinitely
// until all bytes are written or an error occurs.
// Returns the number of bytes written and any error encountered.
func ConnWriteFull(conn net.Conn, buffer []byte, deadline time.Time) (int, error) {
	if !deadline.IsZero() {
		if err := conn.SetWriteDeadline(deadline); err != nil {
			return 0, err
		}

		defer conn.SetWriteDeadline(time.Time{})
	}

	return conn.Write(buffer)
}

func Retry[T any](options RetryOptions[T]) (T, error) {
	var res T
	var err error

	for range options.MaxAttempts {
		res, err = options.Operation()

		if err == nil {
			break
		}

		time.Sleep(options.Delay)
	}

	return res, err
}
