package session

import (
	"fmt"

	"github.com/MlkMahmud/hail/torrent"
)

type session struct {
	id       [20]byte
	torrents map[string]*torrent.Torrent
}

func NewSession(id [20]byte) *session {
	return &session{
		id:       id,
		torrents: map[string]*torrent.Torrent{},
	}
}

func (s *session) AddTorrent(src string, outputDir string) error {
	tr, err := torrent.NewTorrent(torrent.NewTorrentOpts{
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

func (s *session) Stop() {
	for _, tr := range s.torrents {
		tr.Stop()
	}
}
