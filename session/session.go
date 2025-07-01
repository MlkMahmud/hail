package session

import (
	"crypto/sha1"
	"fmt"
	"log/slog"
	"math/rand"
	"time"

	"github.com/MlkMahmud/hail/torrent"
)

type session struct {
	id       [sha1.Size]byte
	torrents map[string]*torrent.Torrent
	logger   *slog.Logger
}

type SessionOpts struct {
	Logger *slog.Logger
}

func generateSessionId() [sha1.Size]byte {
	seededRand := rand.New(rand.NewSource(time.Now().UnixNano()))

	byteArr := [sha1.Size]byte{}
	charset := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

	index := copy(byteArr[:], []byte("-HA001-"))

	for i := index; i < sha1.Size; i++ {
		byteArr[i] = charset[seededRand.Intn(len(charset))]
	}

	return byteArr
}

func NewSession(opts SessionOpts) *session {
	return &session{
		id:       generateSessionId(),
		logger:   opts.Logger,
		torrents: map[string]*torrent.Torrent{},
	}
}

func (s *session) AddTorrent(src string, outputDir string) error {
	tr, err := torrent.NewTorrent(torrent.NewTorrentOpts{
		Logger:    s.logger,
		PeerId:    s.id,
		OutputDir: outputDir,
		Src:       src,
	})

	if err != nil {
		return fmt.Errorf("unable to add torrent to session from source %s: %w", src, err)
	}

	if _, ok := s.torrents[tr.ID()]; ok {
		return nil
	}

	s.torrents[tr.ID()] = tr
	tr.Start()

	return nil
}

func (s *session) ID() string {
	return string(s.id[:])
}

func (s *session) Stop() {
	for _, tr := range s.torrents {
		tr.Stop()
	}
}
