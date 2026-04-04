package main

import (
	"log/slog"
	"os/exec"
	"time"

	"github.com/veandco/go-sdl2/sdl"
)

// Dispatches commands to the MPD server based upon received events
func eventDispatcher(safeClient *SafeMPDClient, events <-chan ControlEvent) {
	renderTicker := time.NewTicker(33 * time.Millisecond) // ~30 FPS
	defer renderTicker.Stop()

	for {
		select {
		case ev, ok := <-events:
			if !ok {
				return
			}
			logger.Info("Dispatch", slog.Any("event", ev))

			mpdClient := safeClient.Get()
			if mpdClient == nil {
				logger.Warn("MPD client not connected, skipping action")
				continue
			}

			switch ev.Action {
			case "play":
				if err := mpdClient.Play(-1); err != nil {
					logger.Error("Play failed", slog.Any("err", err))
				}

			case "pause":
				if err := mpdClient.Pause(true); err != nil {
					logger.Error("Pause failed", slog.Any("err", err))
				}

			case "toggle":
				if err := mpdClient.Pause(false); err != nil {
					logger.Error("Toggle failed", slog.Any("err", err))
				}

			case "stop":
				if err := mpdClient.Stop(); err != nil {
					logger.Error("Stop failed", slog.Any("err", err))
				}

			case "next":
				if err := mpdClient.Next(); err != nil {
					logger.Error("Next failed", slog.Any("err", err))
				}

			case "prev":
				if err := mpdClient.Previous(); err != nil {
					logger.Error("Previous failed", slog.Any("err", err))
				}

			case "seek":
				if err := mpdClient.SeekCur(time.Duration(ev.Value)*time.Second, true); err != nil {
					logger.Error("Seek failed", slog.Any("err", err))
				}

			case "info":
				mpdClient := safeClient.Get()
				if mpdClient == nil {
					logger.Warn("Info requested but MPD unavailable")
					continue
				}
				song, err := mpdClient.CurrentSong()
				if err != nil {
					logger.Error("Failed to fetch current song", slog.Any("err", err))
					continue
				}
				displaySongInfo(SongInfoRequest{
					Artist: song["Artist"],
					Album:  song["Album"],
					Title:  song["Title"],
					Track:  song["Track"],
				})

			case "poweroff":
				logger.Warn("Powering off system...")
				err := exec.Command("systemctl", "poweroff").Run()
				if err != nil {
					logger.Error("Failed to power off", slog.Any("err", err))
				}

			default:
				logger.Warn("Unknown action", slog.Any("action", ev.Action))
			}
		case <-renderTicker.C:
			// Pump SDL events and render in the main thread
			for event := sdl.PollEvent(); event != nil; event = sdl.PollEvent() {
				switch event.(type) {
				case *sdl.QuitEvent:
					logger.Info("SDL window closed")
					return
				}
			}

			// Call render
			if sdlRenderer != nil && sdlRenderer.renderer != nil {
				sdlRenderer.render()
			}
		}
	}
}
