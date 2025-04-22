package torrent

type File struct {
	torrent *Torrent

	Length int
	Name   string
	Offset int

	pieceEndIndex int
	pieceStartIndex int
}

func (f *File) Pieces() []Piece {
	if f.torrent.Info.Files == nil {
		return nil
	}

	totalNumOfPieces := len(f.torrent.Info.Pieces)

	if f.pieceStartIndex < 0 || f.pieceEndIndex >= totalNumOfPieces {
		return nil
	}

	return f.torrent.Info.Pieces[f.pieceStartIndex: f.pieceEndIndex + 1]
}