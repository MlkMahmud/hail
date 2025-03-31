package torrent

import (
	"bytes"
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

func downloadMetadataPiece(p PeerConnection, pieceIndex int) ([]byte, error) {
	if err := p.sendMetadataRequestMessage(pieceIndex); err != nil {
		return nil, err
	}

	piece, err := p.receiveMetadataMessage()

	if err != nil {
		return nil, err
	}

	return piece, nil
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

	infoDict, ok := trrntDict["info"].(map[string]any)

	if !ok {
		return nil, fmt.Errorf("expected the 'info' property of the metainfo dict to be a dict, but got %T", infoDict)
	}

	encodedValue, err := bencode.EncodeValue(infoDict)

	if err != nil {
		return nil, err
	}

	torrent.InfoHash = sha1.Sum([]byte(encodedValue))

	pieces, err := parseTorrentPieces(infoDict)

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

func (t *Torrent) DownloadMetadata() error {
	hasDownloadedAllPieces := false
	index := 0
	metadataBuffer := []byte{}
	peers, err := t.GetPeers()
	numOfPeers := len(peers)

	if err != nil {
		return nil
	}

	for i := 0; i < numOfPeers && !hasDownloadedAllPieces; i++ {
		peerConnection := NewPeerConnection(peers[i], t.InfoHash)

		err := peerConnection.InitPeerConnection()

		if peerConnection.Conn != nil {
			defer peerConnection.Conn.Close()
		}

		if err != nil {
			continue
		}

		for {
			metadataPiece, err := downloadMetadataPiece(*peerConnection, index)

			if err != nil {
				break
			}

			metadataBuffer = append(metadataBuffer, metadataPiece...)
			index += 1

			//  If it is not the last piece of the metadata, it MUST be 16kiB (BlockSize).
			if len(metadataPiece) != BlockSize {
				hasDownloadedAllPieces = true
				break
			}
		}
	}

	if !hasDownloadedAllPieces {
		return fmt.Errorf("failed to download torrent metadata from %d peers", len(peers))
	}

	metadataHash := sha1.Sum(metadataBuffer)

	if !bytes.Equal(t.InfoHash[:], metadataHash[:]) {
		return fmt.Errorf("downloaded metadata hash is does not match torrent info hash")
	}

	decoded, _, err := bencode.DecodeValue(metadataBuffer)

	if err != nil {
		return fmt.Errorf("failed to decode downloaded metadata: %w", err)
	}

	infoDict, ok := decoded.(map[string]any)

	if !ok {
		return fmt.Errorf("expected the decoded payload to be a dict, but got %T", infoDict)
	}

	var torrentInfo TorrentInfo

	if err := mapstructure.Decode(infoDict, &torrentInfo); err != nil {
		return err
	}

	pieces, err := parseTorrentPieces(infoDict)

	if err != nil {
		return err
	}

	torrentInfo.Pieces = pieces
	t.Info = torrentInfo

	return nil
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

func (t *Torrent) getTrackerUrlWithParams() string {
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

	return fmt.Sprintf("%s?%s", t.TrackerUrl, queryString)
}

func (t *Torrent) GetPeers() ([]Peer, error) {
	peerSize := 6
	trackerUrl := t.getTrackerUrlWithParams()

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
