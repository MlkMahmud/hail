# Hail

**Hail** is a modern, experimental BitTorrent client written in Go. It implements core BitTorrent protocol features, supports magnet links, multi-file torrents, and advanced extensions for robust peer-to-peer file sharing.

---

## Features

- **Core BitTorrent Protocol**: Implements the core protocol for peer discovery, piece exchange, and file downloading.
- **Magnet Link Support**: Download torrents using magnet URIs—no need for .torrent files.
- **Multi-file Torrents**: Handles torrents containing multiple files and directories.
- **Files List Parsing**: Correctly parses and downloads all files listed in a torrent.
- **Multitracker Metadata Extension**: Supports torrents with multiple trackers for improved peer discovery.
- **UDP Trackers**: Communicates with trackers over UDP for faster and more efficient peer discovery.
- **Extension Protocols**:
  - **Metadata Exchange**: Supports the extension for peers to send metadata files (BEP 9), enabling magnet link downloads without a .torrent file.
- **Leeching Only**: Currently, Hail supports downloading (leeching) only. Seeding (uploading to others) is planned for future releases.

---

## Usage

### Installation

Clone the repository and build the project:

```sh
git clone https://github.com/MlkMahmud/hail.git && cd hail && make
```

### Command-Line Interface

```
hail [--debug] download --torrent <torrent-file-or-magnet-link-or-http-url> --output-dir <destination-directory>
```

#### Flags

- `--torrent, -t`  
  Path to a `.torrent` file, a magnet link, or a HTTP URL that resolves to a `.torrent` file (required).
- `--output-dir, -o`  
  Destination directory for downloaded files (required).
- `--debug, -d`  
  Enable debug logging output for troubleshooting and development (optional).

---

## Examples

### Downloading with a .torrent file

```sh
./hail download --torrent ./ubuntu.torrent --output-dir ~/Downloads
```

### Downloading with a HTTP URL

```sh
./hail download --torrent "https://cdimage.debian.org/debian-cd/current/amd64/bt-dvd/debian-12.11.0-amd64-DVD-1.iso.torrent" --output-dir ~/Downloads
```


### Downloading with a magnet link

```sh
./hail download --torrent "magnet:?xt=urn:btih:..." --output-dir ~/Downloads
```

### Enabling Debug Logging

```sh
./hail --debug download --torrent ./ubuntu.torrent --output-dir ~/Downloads
```

---

## Roadmap

- [x] Leeching (downloading) support
- [ ] Seeding (uploading) support
- [ ] DHT (Distributed Hash Table) support
- [ ] Improved error handling and peer management
- [ ] Web UI

---

## License

MIT License

---

**Hail** is a work in progress. Contributions and feedback are welcome!