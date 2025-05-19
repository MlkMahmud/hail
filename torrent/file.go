package torrent

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

type file struct {
	// The total length of the file in bytes.
	length int

	// The name or path of the file.
	name string

	// The index of the first piece that contains data for this file.
	pieceStartIndex int

	// The index of the last piece that contains data for this file.
	pieceEndIndex int

	// The byte offset within the first piece where the file's data starts.
	startOffsetInFirstPiece int

	// The byte offset within the last piece where the file's data ends.
	endOffsetInLastPiece int
}

func (f *file) openOrCreate(parentDir string) (*os.File, error) {
	fullPath := filepath.Join(parentDir, f.name)

	// Ensure parent directories exist
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		return nil, fmt.Errorf("failed to create parent directories \"%s\": %w", parentDir, err)
	}

	// Try to open the file for read/write
	fptr, err := os.OpenFile(fullPath, os.O_RDWR, 0644)

	if err == nil {
		return fptr, nil
	}

	if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("failed to open file \"%s\": %w", fullPath, err)
	}

	// File does not exist, create it
	fptr, err = os.Create(fullPath)

	if err != nil {
		return nil, fmt.Errorf("failed to create file \"%s\" : %w", fullPath, err)
	}

	// Ensure the file is the correct length
	if f.length <= 0 {
		return nil, fmt.Errorf("file must have a positive length, got %d", f.length)
	}

	if _, err := fptr.Seek(int64(f.length-1), 0); err != nil {
		fptr.Close()
		return nil, fmt.Errorf("failed to seek when creating file: %w", err)
	}

	if _, err := fptr.Write([]byte{0}); err != nil {
		fptr.Close()
		return nil, fmt.Errorf("failed to set file length: %w", err)
	}

	// Seek back to the beginning for writing
	if _, err := fptr.Seek(0, 0); err != nil {
		fptr.Close()
		return nil, fmt.Errorf("failed to seek to start: %w", err)
	}

	return fptr, nil
}
