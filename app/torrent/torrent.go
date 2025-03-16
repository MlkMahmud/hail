package torrent

import (
	"crypto/sha1"

	"github.com/codecrafters-io/bittorrent-starter-go/app/bencode"
	"github.com/mitchellh/mapstructure"
)

type TorrentInfo struct {
	Length      int    `mapstructure:"length"`
	Name        string `mapstructure:"name"`
	Pieces      string `mapstructure:"pieces"`
	PieceHashes [][sha1.Size]byte
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

	encodedValue, bencodeErr := bencode.EncodeValue(map[string]any{
		"length":       torrent.Info.Length,
		"name":         torrent.Info.Name,
		"piece length": torrent.Info.PieceLength,
		"pieces":       torrent.Info.Pieces,
	})

	if bencodeErr != nil {
		return nil, bencodeErr
	}

	torrent.InfoHash = sha1.Sum([]byte(encodedValue))

	for i := 0; i < len(torrent.Info.Pieces); i += sha1.Size {
		torrent.Info.PieceHashes = append(torrent.Info.PieceHashes, sha1.Sum([]byte(torrent.Info.Pieces[i:sha1.Size+i])))
	}

	return &torrent, nil
}
