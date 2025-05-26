package torrent

import (
	"bytes"
	"crypto/sha1"
	"encoding/binary"
	"fmt"
)

type block struct {
	begin      int
	data       []byte
	length     int
	pieceIndex int
}

type downloadedPiece struct {
	data  []byte
	piece piece
}

type piece struct {
	fileIndexes []int
	index       int
	hash        [sha1.Size]byte
	length      int
}

const (
	blockSize = 16384
)

func generateBlockRequestPayload(block block) []byte {
	blockBeginSize := 4
	blockIndexSize := 4
	blockLengthSize := 4
	messageBufferSize := blockBeginSize + blockIndexSize + blockLengthSize

	messageBuffer := make([]byte, messageBufferSize)

	index := 0

	binary.BigEndian.PutUint32(messageBuffer[index:], uint32(block.pieceIndex))
	index += blockIndexSize

	binary.BigEndian.PutUint32(messageBuffer[index:], uint32(block.begin))
	index += blockBeginSize

	binary.BigEndian.PutUint32(messageBuffer[index:], uint32(block.length))
	index += blockLengthSize

	return messageBuffer
}

func (p *piece) assembleBlocks(blocks []block) []byte {
	buffer := make([]byte, p.length)

	for _, block := range blocks {
		copy(buffer[block.begin:], block.data)
	}

	return buffer
}

func (d *downloadedPiece) validateIntegrity() error {
	downloadedPieceHash := sha1.Sum(d.data)

	if bytes.Equal(downloadedPieceHash[:], d.piece.hash[:]) {
		return nil
	}

	return fmt.Errorf(
		"integrity validation failed for downloaded piece at index '%d':\n"+
			"  - Calculated hash: '%x'\n"+
			"  - Expected hash:   '%x'\n"+
			"  - Piece length:    %d bytes\n"+
			"  - Piece index:     %d",
		d.piece.index,
		downloadedPieceHash,
		d.piece.hash,
		len(d.data),
		d.piece.index,
	)
}

func (p *piece) getBlocks() []block {
	blocks := []block{}

	numOfFullSizedBlocks := int(p.length / blockSize)

	for i := range numOfFullSizedBlocks {
		blocks = append(blocks, block{begin: i * blockSize, length: blockSize, pieceIndex: p.index})
	}

	if lastBlockSize := p.length % blockSize; lastBlockSize != 0 {
		lastBlockIndex := numOfFullSizedBlocks * blockSize
		blocks = append(blocks, block{begin: lastBlockIndex, length: lastBlockSize, pieceIndex: p.index})
	}

	return blocks
}
