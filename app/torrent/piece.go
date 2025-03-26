package torrent

import (
	"crypto/sha1"
	"fmt"
)

type Block struct {
	Begin     int
	Data      []byte
	Length    int
	PiceIndex int
}

type Piece struct {
	Index  int
	Hash   [sha1.Size]byte
	Length int
}

const (
	BlockSize = 16384
)

func parseTorrentPieces(metainfo map[string]any) ([]Piece, error) {
	infoDict, ok := metainfo["info"].(map[string]any)

	if !ok {
		return nil, fmt.Errorf("expected the 'info' property of the metainfo dict to be a dict, but got %T", infoDict)
	}

	pieceHashes, ok := infoDict["pieces"].(string)

	if !ok {
		return nil, fmt.Errorf("expected the 'pieces' property of the info dict to be a string, but got %T", pieceHashes)
	}

	pieceHashesLen := len(pieceHashes)

	if pieceHashesLen == 0 {
		return nil, fmt.Errorf("the 'pieces' property cannot be an empty string")
	}

	if pieceHashesLen%sha1.Size != 0 {
		return nil, fmt.Errorf("pieces length must be a multiple of %d", sha1.Size)
	}

	pieceLen, ok := infoDict["piece length"].(int)

	if !ok {
		return nil, fmt.Errorf("expected the 'piece length' property of the info dict to be an integer, but got %T", pieceLen)
	}

	fileLen, ok := infoDict["length"].(int)

	if !ok {
		return nil, fmt.Errorf("expected the 'length' property of the info dict to be an integer, but got %T", fileLen)
	}

	numOfPieces := pieceHashesLen / sha1.Size
	piecesArr := make([]Piece, numOfPieces)

	for i, j := 0, 0; i < pieceHashesLen; i += sha1.Size {
		pieceHash := []byte(pieceHashes[i : sha1.Size+i])

		if len(pieceHash) != sha1.Size {
			return nil, fmt.Errorf("piece %d has an invalid hash", j)
		}

		piece := Piece{Index: j, Hash: [sha1.Size]byte(pieceHash)}

		// All pieces have the same fixed length, except the last piece which may be truncated
		// The truncated length of the last piece can be generated by subtracting the sum of all other pieces from the total length of the file.
		if j == numOfPieces-1 {
			piece.Length = fileLen - (pieceLen * (numOfPieces - 1))
		} else {
			piece.Length = pieceLen
		}

		piecesArr[j] = piece
		j++
	}

	return piecesArr, nil
}

func (p *Piece) AssembleBlocks(blocks []Block) []byte {
	buffer := make([]byte, p.Length)

	for _, block := range blocks {
		copy(buffer[block.Begin:], block.Data)
	}

	return buffer
}

func (p *Piece) GetBlocks() []Block {
	blocks := []Block{}

	numOfFullSizedBlocks := int(p.Length / BlockSize)

	for i := 0; i < numOfFullSizedBlocks; i++ {
		blocks = append(blocks, Block{Begin: i * BlockSize, Length: BlockSize, PiceIndex: p.Index})
	}

	if lastBlockSize := p.Length % BlockSize; lastBlockSize != 0 {
		lastBlockIndex := numOfFullSizedBlocks * BlockSize
		blocks = append(blocks, Block{Begin: lastBlockIndex, Length: lastBlockSize, PiceIndex: p.Index})
	}

	return blocks
}
