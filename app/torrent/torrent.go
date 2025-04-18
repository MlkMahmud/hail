package torrent

import (
	"bytes"
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/codecrafters-io/bittorrent-starter-go/app/bencode"
	"github.com/codecrafters-io/bittorrent-starter-go/app/utils"
	"github.com/mitchellh/mapstructure"
)

type Peer struct {
	InfoHash  [sha1.Size]byte
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
	if len(magnetLink) == 0 {
		return nil, fmt.Errorf("magnet link cannot be an empty string")
	}

	parsedUrl, err := url.Parse(magnetLink)

	if err != nil {
		return nil, err
	}

	if parsedUrl.Scheme != "magnet" {
		return nil, fmt.Errorf("magnet link URI is invalid")
	}

	params, err := url.ParseQuery(parsedUrl.RawQuery)

	if err != nil {
		return nil, err
	}

	if infoHashParam, ok := params["xt"]; !ok || len(infoHashParam) != 1 {
		return nil, fmt.Errorf("magnet link must include an 'xt' (info hash) paramater")
	}

	if trackerParam, ok := params["tr"]; !ok || len(trackerParam) == 0 {
		return nil, fmt.Errorf("magnet link must include a 'tr' (list of trackers) parameter")
	}

	torrentName := ""

	if nameParam, ok := params["dn"]; ok && len(nameParam) > 0 {
		torrentName = nameParam[0]
	}

	infoHash, err := getInfoHashFromQueryString(params["xt"][0])

	if err != nil {
		return nil, err
	}

	return &Torrent{
		Info: TorrentInfo{
			Name: torrentName,
		},
		InfoHash:   *infoHash,
		TrackerUrl: params["tr"][0],
	}, nil
}

func getPeersOverUDP(t *Torrent) ([]Peer, error) {
	parsedUrl, err := url.Parse(t.TrackerUrl)

	if err != nil {
		return nil, fmt.Errorf("failed to parse tracker URL: %w", err)
	}

	if scheme := parsedUrl.Scheme; scheme != "udp" {
		return nil, fmt.Errorf("tracker scheme must be 'UDP' got '%s'", scheme)
	}

	addr, err := net.ResolveUDPAddr("udp4", fmt.Sprintf("%s:%s", parsedUrl.Host, parsedUrl.Port()))

	if err != nil {
		return nil, fmt.Errorf("failed to resolve tracker URL: %w", err)
	}

	conn, err := net.DialTimeout("udp", addr.String(), 5*time.Second)

	if err != nil {
		return nil, fmt.Errorf("failed to initiate connection with tracker: %w", err)
	}

	defer conn.Close()

	// send connect message
	// receive response
	// send announce message
	// receive annouce response

	return nil, nil
}

func sendConnectRequest(conn net.Conn) (uint32, error) {
	/*
		connect request:
		Offset  Size            Name            Value
		0       64-bit integer  protocol_id     0x41727101980 // magic constant
		8       32-bit integer  action          0 // connect
		12      32-bit integer  transaction_id
		16
	*/
	connectRequestSize := 16
	buffer := make([]byte, connectRequestSize)
	transactionId := rand.Uint32() 
	index := 0

	binary.BigEndian.PutUint64(buffer[index:], 0x41727101980)
	index += 8

	binary.BigEndian.PutUint32(buffer[index:], 0)
	index += 4

	binary.BigEndian.PutUint32(buffer[index:], transactionId)

	
	// wrap this code in retry logic
	if _, err := utils.ConnWriteFull(conn, buffer, 10 * time.Second); err != nil {
		return 0, fmt.Errorf("failed to send connect request to tracker: %w", err)
	}

	responseBuffer := make([]byte, connectRequestSize)

	if _, err := utils.ConnReadFull(conn, responseBuffer, 10 * time.Second); err != nil {
		return 0, fmt.Errorf("failed to received a connect response from tracker: %w", err)
	}


	// todo: parse response and get connection_id 
	return 0, nil
}

func (t *Torrent) DownloadMetadata() error {
	if t.Info.Pieces != nil {
		return nil
	}

	hasDownloadedAllPieces := false
	index := 0
	metadataBuffer := []byte{}
	peers, err := t.GetPeers()

	if err != nil {
		return nil
	}

	for i, numOfPeers := 0, len(peers); i < numOfPeers && !hasDownloadedAllPieces; i++ {
		peerConnection := NewPeerConnection(PeerConnectionConfig{Peer: peers[i]})

		if err := peerConnection.InitConnection(); err != nil {
			peerConnection.Close()
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

func (t *Torrent) parseTrackerResponse(res []byte) ([]Peer, error) {
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

	peersValue, ok := peers.(string)

	if !ok {
		return nil, fmt.Errorf("decoded value of \"peers\" is invalid. expected a string got %T", peers)
	}

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

func (t *Torrent) GetPeers() ([]Peer, error) {
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

	peers, err := t.parseTrackerResponse(trackerResponse)

	if err != nil {
		return nil, err
	}

	return peers, nil
}
