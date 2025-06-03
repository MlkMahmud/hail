package torrent

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/MlkMahmud/hail/utils"
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

func newMessageReader(opts messageReaderOpts) *messageReader {
	return &messageReader{
		conn:     opts.conn,
		errCh:    make(chan error, 1),
		messages: make(chan message, opts.messageBufferSize),
	}
}

func (mr *messageReader) readBuffer(buffer []byte) error {
	_, err := utils.ConnReadFull(mr.conn, buffer, time.Time{})

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

	if messageLength == 0 {
		return message{id: keepAliveMessageId, payload: nil}, nil
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

func (mr *messageReader) run(ctx context.Context) {
	go func(ct context.Context, reader *messageReader) {
		<-ct.Done()
		// unblocks any blocked read operation on the net.conn
		reader.conn.SetReadDeadline(time.Now())
	}(ctx, mr)

	for {
		select {
		case <-ctx.Done():
			return

		default:
			message, err := mr.readMessage()
			if err != nil {
				mr.errCh <- err
				close(mr.errCh)
				return
			}

			if message.id == keepAliveMessageId {
				continue
			}

			select {
			case <-ctx.Done():
				return
			case mr.messages <- message:
			}
		}
	}
}
