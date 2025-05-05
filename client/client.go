package client

// import (
// 	"fmt"

// 	"github.com/MlkMahmud/hail/downloader"
// 	"github.com/MlkMahmud/hail/torrent"
// )

// // func Download(src string, dest string) error {
// // 	trrnt, err := torrent.NewTorrent(src)

// // 	if err != nil {
// // 		return fmt.Errorf("failed to download torrent: %w", err)
// // 	}

// // 	peers, err := trrnt.SendAnnounceRequest()

// // 	if err != nil {
// // 		return fmt.Errorf("failed to download torrent: %w", err)
// // 	}

// // 	var metadataRequestErr error

// // 	if trrnt.Info.Pieces == nil {
// // 		metadataRequestErr = trrnt.DownloadMetadata()
// // 	}

// // 	if metadataRequestErr != nil {
// // 		return fmt.Errorf("failed to download torrent: %w", err)
// // 	}

// // 	dm := downloader.NewDownloadManager()

// // 	dm.Enqueue(downloader.NewDownloadRequest(downloader.DownloadRequestConfig{
// // 		Dest:   dest,
// // 		Name:   trrnt.Info.Name,
// // 		Peers:  peers,
// // 		Pieces: trrnt.Info.Pieces,
// // 	}))

// // 	dm.Start()

// // 	return nil
// // }
