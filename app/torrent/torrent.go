package torrent

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/codecrafters-io/bittorrent-starter-go/app/bencode"
	"github.com/codecrafters-io/bittorrent-starter-go/app/utils"
	"github.com/mitchellh/mapstructure"
)

type Peer struct {
	InfoHash  [sha1.Size]byte
	IpAddress string
	Port      uint16
}

type File struct {
	Length int
	Name   string
	Pieces []Piece
}

type TorrentInfo struct {
	Files  []File
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

func generateTorrentFromFile(src string) (*Torrent, error) {
	var torrent *Torrent

	content, err := os.ReadFile(src)

	if err != nil {
		return torrent, fmt.Errorf("failed to read metainfo file: %w", err)
	}

	decodedValue, _, err := bencode.DecodeValue(content)

	if err != nil {
		return torrent, fmt.Errorf("failed to decode metainfo file: %w", err)
	}

	metainfo, ok := decodedValue.(map[string]any)

	if !ok {
		return torrent, fmt.Errorf("expected metainfo to be a bencoded dictionary, but received '%T'", metainfo)
	}

	for key, value := range map[string]any{"announce": "string", "info": make(map[string]any)} {
		if _, exists := metainfo[key]; !exists {
			return torrent, fmt.Errorf("metainfo dictionary is missing required property '%s'", key)
		}

		expectedType := reflect.TypeOf(value)
		receivedType := reflect.TypeOf(metainfo[key])

		if receivedType != expectedType {
			return torrent, fmt.Errorf("expected the '%s' property to be of type '%v', but received '%v'", key, expectedType, receivedType)
		}
	}

	torrentInfo, err := parseInfoDict(metainfo["info"].(map[string]any))

	if err != nil {
		return torrent, fmt.Errorf("failed to parse metainfo 'info' dictionary %w", err)
	}

	bencodedValue, err := bencode.EncodeValue(metainfo["info"])

	if err != nil {
		return torrent, fmt.Errorf("failed to encode metainfo 'info' dictionary")
	}

	return &Torrent{
		Info:       torrentInfo,
		InfoHash:   sha1.Sum([]byte(bencodedValue)),
		TrackerUrl: metainfo["announce"].(string),
	}, nil
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

func parseInfoDict(infoDict map[string]any) (TorrentInfo, error) {
	var torrentInfo TorrentInfo

	for key, value := range map[string]any{"name": "", "piece length": 0, "pieces": ""} {
		if _, exists := infoDict[key]; !exists {
			return torrentInfo, fmt.Errorf("metainfo 'info' dictionary is missing required property '%s'", key)
		}

		expectedType := reflect.TypeOf(value)
		receivedType := reflect.TypeOf(infoDict[key])

		if receivedType != expectedType {
			return torrentInfo, fmt.Errorf("expected the '%s' property to be of type '%v', but received '%v'", key, expectedType, receivedType)
		}
	}

	if _, ok := infoDict["files"]; ok {
		info, err := parseMultiFileTorrent(infoDict)

		return info, err
	}

	if _, ok := infoDict["length"]; !ok {
		return torrentInfo, fmt.Errorf("metainfo 'info' dictionary must contain a 'files' or 'length' property")
	}

	length, ok := infoDict["length"].(int)

	if !ok {
		return torrentInfo, fmt.Errorf("'length' property of metainfo info dictionary must be an integer not %T", length)
	}

	pieces, err := parsePieces(length, infoDict["pieces"].(string), infoDict["piece length"].(int))

	if err != nil {
		return torrentInfo, err
	}

	files := []File{{
		Length: length,
		Name:   infoDict["name"].(string),
		Pieces: pieces,
	}}

	torrentInfo.Files = files
	return torrentInfo, nil
}

func parseMultiFileTorrent(infoDict map[string]any) (TorrentInfo, error) {
	var torrentInfo TorrentInfo

	fileslist, ok := infoDict["files"].([]any)

	if !ok {
		return torrentInfo, fmt.Errorf("expected 'files' property to be a list, but received '%T'", fileslist)
	}

	numOfFiles := len(fileslist)
	files := make([]File, numOfFiles)

	pieceLength := infoDict["piece length"].(int)
	pieces := infoDict["pieces"].(string)

	for i, piecesIndex := 0, 0; i < numOfFiles; i++ {
		file, ok := fileslist[i].(map[string]any)

		if !ok {
			return torrentInfo, fmt.Errorf("filelist contains an invalid entry at index '%d'", i)
		}

		if _, ok := file["length"].(int); !ok {
			return torrentInfo, fmt.Errorf("filelist entry at index '%d' contains an invalid 'length' property", i)
		}

		if _, ok := file["path"].([]any); !ok {
			return torrentInfo, fmt.Errorf("filelist entry at index '%d' contains an invalid 'path' property", i)
		}

		pathList := make([]string, len(file["path"].([]any)))

		for index, entry := range file["path"].([]any) {
			if _, ok := entry.(string); !ok {
				return torrentInfo, fmt.Errorf("filelist entry at index '%d' contains an invalid 'path' property", i)
			}

			pathList[index] = entry.(string)
		}

		length := file["length"].(int)
		path := filepath.Join(pathList...)
		remainingPieces := pieces[piecesIndex:]
		requiredNumOfPieces := int(math.Ceil(float64(length) / float64(pieceLength)))

		pieces, err := parsePieces(length, remainingPieces, pieceLength)

		if err != nil {
			return torrentInfo, fmt.Errorf("failed to parse filelist entry at index '%d': %w", i, err)
		}

		files[i] = File{
			Length: length,
			Name:   filepath.Join(infoDict["name"].(string), path),
			Pieces: pieces,
		}

		piecesIndex += requiredNumOfPieces * sha1.Size
	}

	torrentInfo.Files = files

	return torrentInfo, nil
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
