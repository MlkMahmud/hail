package torrent

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"net"
	"time"

	"github.com/codecrafters-io/bittorrent-starter-go/app/utils"
)

/*
Announce Request

	Choose a random transaction ID.
	Fill the announce request structure.
	Send the packet.
	IPv4 announce request:

	Offset  Size    Name    Value
	0       64-bit integer  connection_id
	8       32-bit integer  action          1 // announce
	12      32-bit integer  transaction_id
	16      20-byte string  info_hash
	36      20-byte string  peer_id
	56      64-bit integer  downloaded
	64      64-bit integer  left
	72      64-bit integer  uploaded
	80      32-bit integer  event           0 // 0: none; 1: completed; 2: started; 3: stopped
	84      32-bit integer  IP address      0 // default
	88      32-bit integer  key
	92      32-bit integer  num_want        -1 // default
	96      16-bit integer  port
	98
*/
func (tr *Torrent) sendAnnounceRequest(conn net.Conn, connectionId uint64, transactionId uint32) ([]byte, error) {
	action := uint32(1)
	peerId := utils.GenerateRandomString(20, "")
	port := uint16(6881)
	reqBuffer := make([]byte, 98)
	attempts := 0
	index := 0

	binary.BigEndian.PutUint64(reqBuffer, connectionId)
	index += 8

	binary.BigEndian.PutUint32(reqBuffer[index:], action)
	index += 4

	binary.BigEndian.PutUint32(reqBuffer[index:], transactionId)
	index += 4

	index += copy(reqBuffer[index:], tr.InfoHash[:])
	index += copy(reqBuffer[index:], []byte(peerId))

	binary.BigEndian.PutUint64(reqBuffer[index:], 0)
	index += 8

	binary.BigEndian.PutUint64(reqBuffer[index:], 0)
	index += 8

	binary.BigEndian.PutUint64(reqBuffer[index:], 0)
	index += 8

	binary.BigEndian.PutUint32(reqBuffer[index:], 0)
	index += 4

	binary.BigEndian.PutUint32(reqBuffer[index:], 0)
	index += 4

	binary.BigEndian.PutUint32(reqBuffer[index:], 0)
	index += 4

	numWant := -1
	binary.BigEndian.PutUint32(reqBuffer[index:], uint32(numWant))
	index += 4

	binary.BigEndian.PutUint16(reqBuffer[index:], port)

	response, err := utils.Retry(utils.RetryOptions[[]byte]{
		Delay: 3 * time.Second,
		Operation: func() ([]byte, error) {
			defer func() {
				attempts += 1
			}()

			if _, err := utils.ConnWriteFull(conn, reqBuffer, 0); err != nil {
				return nil, fmt.Errorf("failed to send 'announce' request to tracker: %w", err)
			}

			timeout := time.Duration(15 * (int(math.Pow(2, float64(attempts)))))

			if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
				return nil, fmt.Errorf("failed to received 'announce' response from tracker: %w", err)
			}

			resBuffer, err := io.ReadAll(conn)

			if err != nil {
				return nil, fmt.Errorf("failed to receive 'announce' response from tracker: %w", err)
			}

			if expectedSize, receivedSize := 0, len(resBuffer); receivedSize != expectedSize {
				return nil, fmt.Errorf("'announce' response should contain at least '%d", expectedSize)
			}

			if receivedAction := binary.BigEndian.Uint32(resBuffer); receivedAction != action {
				return nil, fmt.Errorf("received action value '%d' does not match expected value '%d'", receivedAction, action)
			}

			if receivedTransactionId := binary.BigEndian.Uint32(resBuffer[4:]); receivedTransactionId != transactionId {
				return nil, fmt.Errorf("received transaction_id '%d' does not match expected value '%d'", receivedTransactionId, transactionId)
			}

			return resBuffer, nil
		},
		MaxAttemps: 3,
	})

	return response, err
}

/*
	connect request:
		Offset  Size            Name            Value
		0       64-bit integer  protocol_id     0x41727101980 // magic constant
		8       32-bit integer  action          0 // connect
		12      32-bit integer  transaction_id
		16
	*/
func sendConnectRequest(conn net.Conn, transactionId uint32) (uint64, error) {
	action := uint32(0)
	connectRequestSize := 16
	reqBuffer := make([]byte, connectRequestSize)
	resBuffer := make([]byte, connectRequestSize)

	index := 0

	binary.BigEndian.PutUint64(reqBuffer[index:], 0x41727101980)
	index += 8

	binary.BigEndian.PutUint32(reqBuffer[index:], action)
	index += 4

	binary.BigEndian.PutUint32(reqBuffer[index:], transactionId)

	attempts := 0

	connectionId, err := utils.Retry(utils.RetryOptions[uint64]{
		Delay: 3 * time.Second,
		Operation: func() (uint64, error) {
			defer func() {
				attempts += 1
			}()

			if _, err := utils.ConnWriteFull(conn, reqBuffer, 5*time.Second); err != nil {
				return 0, fmt.Errorf("failed to send 'connect' message request to tracker: %w", err)
			}

			/*
				If a response is not received after 15 * 2 ^ n seconds,
				the client should retransmit the request, where n starts at 0 and is increased up to 8 (3840 seconds) after every retransmission.
			*/
			timeout := time.Duration(15 * (int(math.Pow(2, float64(attempts)))))

			if _, err := utils.ConnReadFull(conn, resBuffer, timeout); err != nil {
				return 0, fmt.Errorf("failed to receive 'connect' message response from tracker: %w", err)
			}

			if receivedAction := binary.BigEndian.Uint32(resBuffer); receivedAction != action {
				return 0, fmt.Errorf("received action value '%d' does not match expected value '%d'", receivedAction, action)
			}

			if receivedTransactionId := binary.BigEndian.Uint32(resBuffer[4:]); receivedTransactionId != transactionId {
				return 0, fmt.Errorf("received transaction_id '%d' does not match expected value '%d'", receivedTransactionId, transactionId)
			}

			return binary.BigEndian.Uint64(resBuffer[8:]), nil
		},
		MaxAttemps: 4,
	})

	return connectionId, err
}
