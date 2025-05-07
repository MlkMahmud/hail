package torrent

import (
	"context"
	"crypto/sha1"
	"encoding/base32"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"

	"github.com/MlkMahmud/hail/utils"
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

func parseMagnetURL(magnetURL *url.URL) (Torrent, error) {
	var torrent Torrent

	if magnetURL.Scheme != "magnet" {
		return torrent, fmt.Errorf("URL scheme is invalid. expected \"magnet\" got \"%s\"", magnetURL.Scheme)
	}

	params, err := url.ParseQuery(magnetURL.RawQuery)

	if err != nil {
		return torrent, err
	}

	if infoHashParam, ok := params["xt"]; !ok || len(infoHashParam) != 1 {
		return torrent, fmt.Errorf("magnet URL must include an 'xt' (info hash) parameter")
	}

	if trackerParam, ok := params["tr"]; !ok || len(trackerParam) == 0 {
		return torrent, fmt.Errorf("magnet URL must include a 'tr' (list of trackers) parameter")
	}

	infoHash, err := parseInfoHashParameter(params["xt"][0])

	if err != nil {
		return torrent, err
	}

	trackers := utils.NewSet()

	for _, tr := range params["tr"] {
		trackers.Add(tr)
	}

	ctx, cancelFunc := context.WithCancel(context.Background())

	torrent.ctx = ctx
	torrent.cancelFunc = cancelFunc

	torrent.infoHash = infoHash
	torrent.incomingPeersCh = make(chan []Peer, 1)
	torrent.maxPeerConnections = 10
	torrent.metadataPeersCh = make(chan PeerConnection, 10)
	torrent.peerConnections = map[string]PeerConnection{}
	torrent.peers = make(map[string]Peer)
	torrent.failingPeers = make(map[string]Peer)
	torrent.statusCh = make(chan torrentStatus, 1)
	torrent.trackers = *trackers

	return torrent, nil
}
