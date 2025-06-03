package torrent

import (
	"context"
	"encoding/binary"
	"net"
	"time"

	"github.com/MlkMahmud/hail/utils"
)

type messageWriter struct {
	conn     net.Conn
	errCh    chan error
	messages chan message
}

type messageWriterOpts struct {
	conn              net.Conn
	messageBufferSize int
}

func newMessageWriter(opts messageWriterOpts) *messageWriter {
	return &messageWriter{
		conn:     opts.conn,
		errCh:    make(chan error, 1),
		messages: make(chan message, opts.messageBufferSize),
	}
}

func (mw *messageWriter) writeBuffer(buffer []byte, deadline time.Time) error {
	if _, err := utils.ConnWriteFull(mw.conn, buffer, deadline); err != nil {
		return err
	}

	return nil
}

func (mw *messageWriter) writeMessage(m message, deadline time.Time) error {
	messageIdLen := 1
	messagePrefixLen := 4
	payloadLen := 0

	if m.payload != nil {
		payloadLen = len(m.payload)
	}

	messageBufferLen := messagePrefixLen + messageIdLen + payloadLen
	messageBuffer := make([]byte, messageBufferLen)
	binary.BigEndian.PutUint32(messageBuffer, uint32(messageIdLen+payloadLen))

	index := 4
	messageBuffer[index] = byte(m.id)
	copy(messageBuffer[index+1:], m.payload)

	if err := mw.writeBuffer(messageBuffer, deadline); err != nil {
		return err
	}

	return nil
}

func (mw *messageWriter) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return

		case message, ok := <-mw.messages:
			if !ok {
				return
			}

			// todo: make deadlines configurable
			err := mw.writeMessage(message, time.Now().Add(5*time.Second))

			if err != nil {
				mw.errCh <- err
				close(mw.errCh)
				return
			}
		}
	}
}
