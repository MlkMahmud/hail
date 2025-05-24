package torrent

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
)

type messageReader struct {
	conn     net.Conn
	errCh    chan error
	messages chan message
}

type messageReaderOpts struct {
	conn              net.Conn
	messageBufferSize int
}

const (
	maxMessageLength = 16 * 1024
)

func newMessageReader(opts messageReaderOpts) *messageReader {
	return &messageReader{
		conn:     opts.conn,
		errCh:    make(chan error, 1),
		messages: make(chan message, opts.messageBufferSize),
	}
}

func (mr *messageReader) readBuffer(buffer []byte) error {
	_, err := io.ReadFull(mr.conn, buffer)

	if errors.Is(err, io.EOF) {
		return fmt.Errorf("remote peer closed the connection")
	}

	if err != nil {
		return fmt.Errorf("failed to read from connection: %w", err)
	}

	return nil
}

func (mr *messageReader) readMessage() (message, error) {
	messageLengthBuffer := make([]byte, 4)

	if err := mr.readBuffer(messageLengthBuffer); err != nil {
		return message{}, err
	}

	messageLength := binary.BigEndian.Uint32(messageLengthBuffer)

	if messageLength > maxMessageLength {
		return message{}, fmt.Errorf("message length %d exceeds maximum allowed length %d", messageLength, maxMessageLength)
	}

	messageBuffer := make([]byte, messageLength)

	if err := mr.readBuffer(messageBuffer); err != nil {
		return message{}, err
	}

	return message{
		id:      messageId(messageBuffer[0]),
		payload: messageBuffer[1:],
	}, nil
}

func (mr *messageReader) run() {
	for {
		message, err := mr.readMessage()

		if err != nil {
			mr.errCh <- err
			close(mr.errCh)
			return
		}

		mr.messages <- message
	}
}
