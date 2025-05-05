package torrent

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"time"

	"github.com/MlkMahmud/hail/bencode"
	"github.com/MlkMahmud/hail/utils"
)

func (tr *Torrent) GetPeers() ([]Peer, error) {
	peersList := []Peer{}

	for _, trackerUrl := range tr.Trackers {
		parsedURL, err := url.Parse(trackerUrl)

		if err != nil {
			fmt.Printf("failed to parse tracker URL: %v\n", err)
			continue
		}

		switch parsedURL.Scheme {
		case "http", "https":
			{
				peers, err := tr.getPeersOverHTTP(trackerUrl)

				if err != nil {
					fmt.Println(err.Error())
					continue
				}

				peersList = append(peersList, peers...)
				break
			}

		case "udp":
			{
				peers, err := tr.getPeersOverUDP(trackerUrl)

				if err != nil {
					fmt.Println(err.Error())
					continue
				}

				peersList = append(peersList, peers...)
				break
			}

		default:
			{
				fmt.Println("tracker URL protocol must be one of 'HTTP' or 'UDP'")
			}
		}
	}

	return peersList, nil
}

func (t *Torrent) getPeersOverHTTP(url string) ([]Peer, error) {
	trackerUrl := t.getTrackerUrlWithParams(url)

	req, err := http.NewRequest("GET", trackerUrl, nil)

	if err != nil {
		return nil, err
	}

	res, err := http.DefaultClient.Do(req)

	if err != nil {
		return nil, err
	}

	defer res.Body.Close()
	var trackerResponse []byte

	if res.StatusCode == http.StatusOK {
		trackerResponse, err = io.ReadAll(res.Body)

		if err != nil {
			return nil, err
		}
	}

	peers, err := t.parseHTTPAnnounceResponse(trackerResponse)

	if err != nil {
		return nil, err
	}

	return peers, nil
}

func (tr *Torrent) getPeersOverUDP(trackerUrl string) ([]Peer, error) {
	parsedUrl, err := url.Parse(trackerUrl)

	if err != nil {
		return nil, fmt.Errorf("failed to parse tracker URL: %w", err)
	}

	if scheme := parsedUrl.Scheme; scheme != "udp" {
		return nil, fmt.Errorf("tracker scheme must be 'UDP' got '%s'", scheme)
	}

	addr, err := net.ResolveUDPAddr("udp", parsedUrl.Host)

	if err != nil {
		return nil, fmt.Errorf("failed to resolve tracker URL: %w", err)
	}

	conn, err := net.DialTimeout("udp", addr.String(), 5*time.Second)

	if err != nil {
		return nil, fmt.Errorf("failed to initiate connection with tracker: %w", err)
	}

	defer conn.Close()

	transactionId := rand.Uint32()

	// send connect message
	connectionId, err := sendUDPConnectRequest(conn, transactionId)

	if err != nil {
		return nil, fmt.Errorf("failed to get list of peers: %w", err)
	}

	peers, err := tr.sendUDPAnnounceRequest(conn, connectionId, transactionId)

	if err != nil {
		return nil, fmt.Errorf("failed to get list of peers: %w", err)
	}

	return peers, nil
}

func (t *Torrent) getTrackerUrlWithParams(trackerUrl string) string {
	params := url.Values{}
	length := t.Info.Length

	if length == 0 {
		// set length to a random value if the length of the torrent file is not known yet
		length = 999
	}

	params.Add("info_hash", string(t.InfoHash[:]))
	params.Add("peer_id", utils.GenerateRandomString(20, ""))
	params.Add("port", "6881")
	params.Add("downloaded", "0")
	params.Add("uploaded", "0")
	params.Add("left", strconv.Itoa(length))
	params.Add("compact", "1")

	queryString := params.Encode()

	return fmt.Sprintf("%s?%s", trackerUrl, queryString)
}

func (t *Torrent) parseHTTPAnnounceResponse(res []byte) ([]Peer, error) {
	decodedResponse, _, err := bencode.DecodeValue(res)

	if err != nil {
		return nil, fmt.Errorf("failed to decoded tracker response: %w", err)
	}

	dict, ok := decodedResponse.(map[string]any)

	if !ok {
		return nil, fmt.Errorf("decoded response type \"%T\" is invalid", decodedResponse)
	}

	if failureMsg, ok := dict["failure reason"].(string); ok {
		return nil, fmt.Errorf("failed to get list of peers: %s", failureMsg)
	}

	if warningMsg, ok := dict["warning message"].(string); ok {
		fmt.Println(warningMsg)
	}

	peers, exists := dict["peers"]

	if !exists {
		return nil, fmt.Errorf("decoded response does not include a \"peers\" key")
	}

	switch peersValue := peers.(type) {
	case string:
		{
			peersStringLen := len(peersValue)
			peerSize := 6

			if peersStringLen%peerSize != 0 {
				return nil, fmt.Errorf("peers value must be a multiple of '%d' bytes", peerSize)
			}

			numOfPeers := peersStringLen / peerSize
			peersArr := make([]Peer, numOfPeers)

			for i, j := 0, 0; i < peersStringLen; i += peerSize {
				IpAddress := fmt.Sprintf("%d.%d.%d.%d", byte(peersValue[i]), byte(peersValue[i+1]), byte(peersValue[i+2]), byte(peersValue[i+3]))
				Port := binary.BigEndian.Uint16([]byte(peersValue[i+4 : i+6]))
				peersArr[j] = Peer{IpAddress: IpAddress, Port: Port, InfoHash: t.InfoHash}
				j++
			}

			return peersArr, nil
		}
	case []any:
		{
			peersArr := make([]Peer, len(peersValue))

			for index, peer := range peersValue {
				peerDict, ok := peer.(map[string]any)

				if !ok {
					return nil, fmt.Errorf("peers list contains an invalid entry at index: \"%d\"", index)
				}

				for key, value := range map[string]any{"ip": "", "port": 0} {
					if _, exists := peerDict[key]; !exists {
						return nil, fmt.Errorf("peers list entry at index '%d' is missing required property \"%s\"", index, key)
					}

					expectedType := reflect.TypeOf(value)
					receivedType := reflect.TypeOf(peerDict[key])

					if receivedType != expectedType {
						return nil, fmt.Errorf("peers list entry at index '%d' contains an invalid \"%s\" property", index, key)
					}
				}

				peersArr[index] = Peer{
					InfoHash:  t.InfoHash,
					IpAddress: peerDict["ip"].(string),
					Port:      uint16(peerDict["port"].(int)),
				}
			}

			return peersArr, nil
		}

	default:
		{
			return nil, fmt.Errorf("decoded value of \"peers\" is invalid. expected a string or a list of dictionaries, but received %T", peersValue)
		}
	}
}

/*
IPv4 announce response:

	Offset      Size            Name            Value
	0           32-bit integer  action          1 // announce
	4           32-bit integer  transaction_id
	8           32-bit integer  interval
	12          32-bit integer  leechers
	16          32-bit integer  seeders
	20 + 6 * n  32-bit integer  IP address
	24 + 6 * n  16-bit integer  TCP port
	20 + 6 * N
*/
func (tr *Torrent) parseUDPAnnounceResponse(response []byte, action uint32, transactionId uint32) ([]Peer, error) {
	minSize := 20
	peerSize := 6

	if receivedSize := len(response); receivedSize < minSize {
		return nil, fmt.Errorf("'announce' response should contain at least %d bytes", minSize)
	}

	if receivedAction := binary.BigEndian.Uint32(response); receivedAction != action {
		return nil, fmt.Errorf("received action value '%d' does not match expected value '%d'", receivedAction, action)
	}

	if receivedTransactionId := binary.BigEndian.Uint32(response[4:]); receivedTransactionId != transactionId {
		return nil, fmt.Errorf("received transaction_id '%d' does not match expected value '%d'", receivedTransactionId, transactionId)
	}

	peersBuffer := response[minSize : minSize+1]
	peersBufferSize := len(peersBuffer)

	if peersBufferSize%peerSize != 0 {
		return nil, fmt.Errorf("peers list must be a multiple of '%d'", peerSize)
	}

	numOfPeers := peersBufferSize / peerSize
	peersArr := make([]Peer, numOfPeers)

	for i, j := 0, 0; i < peersBufferSize; i += peerSize {
		ipAddresss := fmt.Sprintf("%d.%d.%d.%d", peersBuffer[i], peersBuffer[i+1], peersBuffer[i+2], peersBuffer[i+3])
		port := binary.BigEndian.Uint16(peersBuffer[i+4:])
		peersArr[j] = Peer{InfoHash: tr.InfoHash, IpAddress: ipAddresss, Port: port}
		j++
	}

	return peersArr, nil
}

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
func (tr *Torrent) sendUDPAnnounceRequest(conn net.Conn, connectionId uint64, transactionId uint32) ([]Peer, error) {
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

	response, err := utils.Retry(utils.RetryOptions[[]Peer]{
		Delay: 3 * time.Second,
		Operation: func() ([]Peer, error) {
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

			peers, err := tr.parseUDPAnnounceResponse(resBuffer, action, transactionId)

			return peers, err
		},
		MaxAttempts: 3,
	})

	return response, err
}

/*
Sends a connect request to a UDP tracker.

connect request:

	Offset  Size            Name            Value
	0       64-bit integer  protocol_id     0x41727101980 // magic constant
	8       32-bit integer  action          0 // connect
	12      32-bit integer  transaction_id
	16
*/
func sendUDPConnectRequest(conn net.Conn, transactionId uint32) (uint64, error) {
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
		MaxAttempts: 4,
	})

	return connectionId, err
}
