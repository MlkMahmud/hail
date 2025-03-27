package torrent

import (
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/codecrafters-io/bittorrent-starter-go/app/bencode"
	"github.com/codecrafters-io/bittorrent-starter-go/app/utils"
	"github.com/mitchellh/mapstructure"
)

type Peer struct {
	IpAddress string
	Port      uint16
}

type TorrentInfo struct {
	Length int     `mapstructure:"length"`
	Name   string  `mapstructure:"name"`
	Pieces []Piece `mapstructure:"-"`
}

type Torrent struct {
	Info       TorrentInfo `mapstructure:"info"`
	InfoHash   [sha1.Size]byte
	TrackerUrl string `mapstructure:"announce"`
}

func getInfoHashFromQueryString(queryString string) (*[sha1.Size]byte, error) {
	expectedInfoHashStringLength := 40
	infoHashPrefix := "urn:btih:"

	if !strings.HasPrefix(queryString, infoHashPrefix) {
		return nil, fmt.Errorf("info hash value contains an invalid prefix. expected '%s' got '%s'", infoHashPrefix, queryString)
	}

	infoHashString := queryString[len(infoHashPrefix):]

	if infoHashStringLength := len(infoHashString); infoHashStringLength != expectedInfoHashStringLength {
		return nil, fmt.Errorf("hex encoded info hash string length should be '%d' long, but received string length is %d", expectedInfoHashStringLength, infoHashStringLength)
	}

	decodedString, err := hex.DecodeString(infoHashString)

	if err != nil {
		return nil, fmt.Errorf("failed to decoded hex encoded string %w", err)
	}

	if decodedStringLength := len(decodedString); decodedStringLength != sha1.Size {
		return nil, fmt.Errorf("decoded info hash string length should be '%d' long, but received string length is %d", sha1.Size, decodedStringLength)
	}

	infoHash := [sha1.Size]byte(decodedString)

	return &infoHash, nil
}

func generateTorrentFromFile(torrentFilepath string) (*Torrent, error) {
	fileContent, err := os.ReadFile(torrentFilepath)

	if err != nil {
		return nil, err
	}

	decodedValue, _, err := bencode.DecodeValue(fileContent)

	if err != nil {
		return nil, err
	}

	trrntDict, ok := decodedValue.(map[string]any)

	if !ok {
		return nil, fmt.Errorf("expected decoded object to be a dict got %T", decodedValue)
	}

	var torrent Torrent

	if err := mapstructure.Decode(trrntDict, &torrent); err != nil {
		return nil, err
	}

	encodedValue, err := bencode.EncodeValue(trrntDict["info"])

	if err != nil {
		return nil, err
	}

	torrent.InfoHash = sha1.Sum([]byte(encodedValue))

	pieces, err := parseTorrentPieces(trrntDict)

	if err != nil {
		return nil, err
	}

	torrent.Info.Pieces = pieces

	return &torrent, nil
}

func generateTorrentFromMagnetLink(magnetLink string) (*Torrent, error) {
	parsedUrl, err := url.Parse(magnetLink)

	if err != nil {
		return nil, err
	}

	params, err := url.ParseQuery(parsedUrl.RawQuery)

	if err != nil {
		return nil, err
	}

	for key, value := range map[string]string{"dn": "name", "tr": "tracker url", "xt": "info hash"} {
		received, ok := params[key]

		if !ok || len(received) != 1 {
			return nil, fmt.Errorf("magnet link '%s' parameter is invalid. received value %v", value, received)
		}
	}

	infoHash, err := getInfoHashFromQueryString(params["xt"][0])

	if err != nil {
		return nil, err
	}

	return &Torrent{
		Info: TorrentInfo{
			Name: params["dn"][0],
		},
		InfoHash:   *infoHash,
		TrackerUrl: params["tr"][0],
	}, nil
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

func NewTorrent(torrentFileOrMagnetLink string) (*Torrent, error) {
	var torrent *Torrent
	var err error

	if utils.CheckIfFileExists(torrentFileOrMagnetLink) {
		torrent, err = generateTorrentFromFile(torrentFileOrMagnetLink)

		return torrent, err
	}

	torrent, err = generateTorrentFromMagnetLink(torrentFileOrMagnetLink)

	return torrent, err
}
