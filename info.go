package main

type SongInfoRequest struct {
	Artist string
	Album  string
	Title  string
	Track  string
}

var songInfoChan = make(chan SongInfoRequest, 1)

func startSongInfoDisplay() {
	go func() {
		for info := range songInfoChan {
			showSongInfo(info.Artist, info.Album, info.Title, info.Track)
		}
	}()
}

func displaySongInfo(info SongInfoRequest) {
	select {
	case songInfoChan <- info:
	default:
		// Drop if channel is full to avoid blocking
	}
}
