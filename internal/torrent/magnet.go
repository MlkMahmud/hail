package torrent

import (
	"crypto/sha1"
	"encoding/base32"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"

	"github.com/MlkMahmud/hail/internal/utils"
)

func parseInfoHashParameter(xtParameter string) ([sha1.Size]byte, error) {
	var infoHash [sha1.Size]byte
	expectedHexEncodedLength := 40
	expectedBase32EncodedLength := 32
	infoHashURNPrefix := "urn:btih:"

	if !strings.HasPrefix(xtParameter, infoHashURNPrefix) {
		return infoHash, fmt.Errorf("info hash parameter contains an invalid prefix. expected '%s' got '%s'", infoHashURNPrefix, xtParameter)
	}

	encodedInfoHash := xtParameter[len(infoHashURNPrefix):]
	encodedInfoHashLength := len(encodedInfoHash)

	switch encodedInfoHashLength {
	case expectedHexEncodedLength:
		{
			decodedInfoHash, err := hex.DecodeString(encodedInfoHash)
			if err != nil {
				return infoHash, fmt.Errorf("failed to decode hex encoded info hash: %w", err)
			}
			copy(infoHash[:], decodedInfoHash)
		}
	case expectedBase32EncodedLength:
		{
			decodedInfoHash, err := base32.StdEncoding.DecodeString(encodedInfoHash)
			if err != nil {
				return infoHash, fmt.Errorf("failed to decode base32 encoded info hash: %w", err)
			}
			copy(infoHash[:], decodedInfoHash)
		}
	default:
		{
			return infoHash, fmt.Errorf("info hash must be %d or %d characters long, but received value is %d characters long", expectedHexEncodedLength, expectedBase32EncodedLength, encodedInfoHashLength)
		}
	}

	return infoHash, nil
}

func newTorrentFromMagnetURL(magnetURL *url.URL, opts NewTorrentOpts) (*Torrent, error) {
	var torrent Torrent

	if magnetURL.Scheme != "magnet" {
		return nil, fmt.Errorf("URL scheme is invalid. expected \"magnet\" got \"%s\"", magnetURL.Scheme)
	}

	params, err := url.ParseQuery(magnetURL.RawQuery)

	if err != nil {
		return nil, err
	}

	if infoHashParam, ok := params["xt"]; !ok || len(infoHashParam) != 1 {
		return nil, fmt.Errorf("magnet URL must include an 'xt' (info hash) parameter")
	}

	if trackerParam, ok := params["tr"]; !ok || len(trackerParam) == 0 {
		return nil, fmt.Errorf("magnet URL must include a 'tr' (list of trackers) parameter")
	}

	infoHash, err := parseInfoHashParameter(params["xt"][0])

	if err != nil {
		return nil, err
	}

	trackers := utils.NewSet()

	for _, tr := range params["tr"] {
		trackers.Add(tr)
	}

	torrent.infoHash = infoHash
	torrent.logger = opts.Logger
	torrent.peerId = opts.PeerId
	torrent.outputDir = opts.OutputDir

	torrent.metadataDownloadCompletedCh = make(chan struct{})
	torrent.piecesDownloadCompleteCh = make(chan struct{})

	torrent.incomingPeersCh = make(chan []*peer, 1)
	torrent.maxPeerConnections = 10

	torrent.pendingPeerRequests = make(map[string]chan struct{})
	torrent.peers = make(map[string]*peer)

	torrent.triggerAnnounceCh = make(chan struct{}, 1)

	torrent.downloadedPieceCh = make(chan downloadedPiece, 10)
	torrent.failedPiecesCh = make(chan piece, 10)
	torrent.queuedPiecesCh = make(chan piece, 10)

	torrent.statusCh = make(chan torrentStatus, 1)

	torrent.trackers = *trackers

	return &torrent, nil
}
