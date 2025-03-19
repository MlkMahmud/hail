package torrent

import (
	"bufio"
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/codecrafters-io/bittorrent-starter-go/app/bencode"
	"github.com/codecrafters-io/bittorrent-starter-go/app/utils"
	"github.com/mitchellh/mapstructure"
)

type Peer struct {
	IpAddress string
	Port      uint16
}
type TorrentInfo struct {
	Length      int    `mapstructure:"length"`
	Name        string `mapstructure:"name"`
	Pieces      string `mapstructure:"pieces"`
	PieceHashes [][]byte
	PieceLength int `mapstructure:"piece length"`
}

type Torrent struct {
	Info       TorrentInfo `mapstructure:"info"`
	InfoHash   [sha1.Size]byte
	TrackerUrl string `mapstructure:"announce"`
}

func NewTorrent(value any) (*Torrent, error) {

	var torrent Torrent
	err := mapstructure.Decode(value, &torrent)

	if err != nil {
		return nil, err
	}

	encodedValue, err := bencode.EncodeValue(map[string]any{
		"length":       torrent.Info.Length,
		"name":         torrent.Info.Name,
		"piece length": torrent.Info.PieceLength,
		"pieces":       torrent.Info.Pieces,
	})

	if err != nil {
		return nil, err
	}

	torrent.InfoHash = sha1.Sum([]byte(encodedValue))

	for i := 0; i < len(torrent.Info.Pieces); i += sha1.Size {
		torrent.Info.PieceHashes = append(torrent.Info.PieceHashes, []byte(torrent.Info.Pieces[i:sha1.Size+i]))
	}

	return &torrent, nil
}

func (t *Torrent) ConnectToPeer(peer Peer) ([]byte, error) {
	conn, err := net.Dial("tcp", net.JoinHostPort(peer.IpAddress, strconv.Itoa(int(peer.Port))))

	if err != nil {
		return nil, err
	}

	conn.SetDeadline(time.Now().Add(3 * time.Second))
	defer conn.SetDeadline(time.Time{}) //
	defer conn.Close()

	peerId := []byte(utils.GenerateRandomString(20, ""))
	pstr := "BitTorrent protocol"
	messageBuf := make([]byte, len(pstr)+49)
	messageBuf[0] = byte(len(pstr))

	index := 1
	index += copy(messageBuf[index:], []byte(pstr))
	index += copy(messageBuf[index:], make([]byte, 8))
	index += copy(messageBuf[index:], t.InfoHash[:])
	index += copy(messageBuf[index:], peerId[:])

	_, writeErr := conn.Write(messageBuf)

	if writeErr != nil {
		return nil, writeErr
	}

	respBuf := make([]byte, cap(messageBuf))
	reader := bufio.NewReader(conn)

	if _, err := io.ReadFull(reader, respBuf); err != nil {
		return nil, err
	}

	return respBuf, nil
}

func (t *Torrent) getTrackerUrlWithParams() string {
	params := url.Values{}

	params.Add("info_hash", string(t.InfoHash[:]))
	params.Add("peer_id", utils.GenerateRandomString(20, ""))
	params.Add("port", "6881")
	params.Add("downloaded", "0")
	params.Add("uploaded", "0")
	params.Add("left", strconv.Itoa(t.Info.Length))
	params.Add("compact", "1")

	queryString := params.Encode()

	return fmt.Sprintf("%s?%s", t.TrackerUrl, queryString)

}

func (t *Torrent) GetPeers() ([]Peer, error) {
	peerSize := 6
	trackerUrl := t.getTrackerUrlWithParams()

	req, err := http.NewRequest("GET", trackerUrl, nil)

	if err != nil {
		return nil, err
	}

	client := http.Client{}
	res, err := client.Do(req)

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

	decodedResponse, _, err := bencode.DecodeValue(trackerResponse)

	if err != nil {
		return nil, err
	}

	dict, ok := decodedResponse.(map[string]any)

	if !ok {
		return nil, fmt.Errorf("decoded response type \"%T\" is invalid", decodedResponse)
	}

	peers, exists := dict["peers"]

	if !exists {
		return nil, fmt.Errorf("decoded response does not include a \"peers\" key")
	}

	peersValue, ok := peers.(string)

	if !ok {
		return nil, fmt.Errorf("decoded value of \"peers\" is invalid. expected a string got %T", peers)
	}

	peersStringLen := len(peersValue)

	if peersStringLen%peerSize != 0 {
		return nil, fmt.Errorf("peers value must be a multiple of '%d' bytes", peerSize)
	}

	numOfPeers := peersStringLen / peerSize
	peersArr := make([]Peer, numOfPeers)

	for i, j := 0, 0; i < peersStringLen; i += peerSize {
		IpAddress := fmt.Sprintf("%d.%d.%d.%d", byte(peersValue[i]), byte(peersValue[i+1]), byte(peersValue[i+2]), byte(peersValue[i+3]))
		Port := binary.BigEndian.Uint16([]byte(peersValue[i+4 : i+6]))
		peersArr[j] = Peer{IpAddress, Port}

		j++
	}

	return peersArr, nil
}
