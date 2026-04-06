package main

import (
	"log/slog"
	"os/exec"
	"strconv"
	"time"

	"github.com/veandco/go-sdl2/sdl"
)

func dispatchControlEvent(safeClient *SafeMPDClient, ev ControlEvent) {
	logger.Info("Dispatch", slog.Any("event", ev))

	mpdClient := safeClient.Get()
	if mpdClient == nil {
		logger.Warn("MPD client not connected, skipping action")
		return
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
		status, err := mpdClient.Status()
		if err != nil {
			logger.Warn("Status failed before prev, falling back to previous", slog.Any("err", err))
			if err := mpdClient.Previous(); err != nil {
				logger.Error("Previous failed", slog.Any("err", err))
			}
			break
		}

		elapsed, err := strconv.ParseFloat(status["elapsed"], 64)
		if err != nil {
			logger.Warn("Invalid elapsed time, falling back to previous", "elapsed", status["elapsed"], slog.Any("err", err))
			if err := mpdClient.Previous(); err != nil {
				logger.Error("Previous failed", slog.Any("err", err))
			}
			break
		}

		if elapsed > 10 {
			if err := mpdClient.SeekCur(0, false); err != nil {
				logger.Error("Seek to start failed", slog.Any("err", err))
			}
		} else {
			if err := mpdClient.Previous(); err != nil {
				logger.Error("Previous failed", slog.Any("err", err))
			}
		}

	case "seek":
		if err := mpdClient.SeekCur(time.Duration(ev.Value)*time.Second, true); err != nil {
			logger.Error("Seek failed", slog.Any("err", err))
		}

	case "info":
		song, err := mpdClient.CurrentSong()
		if err != nil {
			logger.Error("Failed to fetch current song", slog.Any("err", err))
			return
		}
		ShowSongInfo(song["Artist"], song["Album"], song["Title"], song["Track"])

	case "poweroff":
		// Pause before poweroff because state is saved.
		if err := mpdClient.Pause(true); err != nil {
			logger.Error("Pause failed", slog.Any("err", err))
		}
		logger.Warn("Powering off system...")
		err := exec.Command("sudo", "systemctl", "poweroff").Run()
		if err != nil {
			logger.Error("Failed to power off", slog.Any("err", err))
		}

	default:
		logger.Warn("Unknown action", slog.Any("action", ev.Action))
	}

	if ev.Source == "input" || ev.Source == "sdl" {
		RefreshSongInfo()
	}
}

func mapSDLKeyToControlEvent(keyCode sdl.Keycode) (ControlEvent, bool) {
	switch keyCode {
	case sdl.K_SPACE, sdl.K_KP_ENTER:
		return ControlEvent{Source: "sdl", Action: "toggle"}, true

	case sdl.K_p, sdl.K_AUDIOPLAY:
		return ControlEvent{Source: "sdl", Action: "play"}, true

	case sdl.K_u, sdl.K_PAUSE:
		return ControlEvent{Source: "sdl", Action: "pause"}, true

	case sdl.K_s, sdl.K_STOP, sdl.K_AUDIOSTOP:
		return ControlEvent{Source: "sdl", Action: "stop"}, true

	case sdl.K_PAGEUP, sdl.K_AUDIONEXT:
		return ControlEvent{Source: "sdl", Action: "next"}, true

	case sdl.K_PAGEDOWN, sdl.K_AUDIOPREV:
		return ControlEvent{Source: "sdl", Action: "prev"}, true

	case sdl.K_RIGHTBRACKET, sdl.K_RIGHT, sdl.K_AC_FORWARD:
		return ControlEvent{Source: "sdl", Action: "seek", Value: 10}, true

	case sdl.K_LEFTBRACKET, sdl.K_LEFT, sdl.K_AC_BACK:
		return ControlEvent{Source: "sdl", Action: "seek", Value: -10}, true

	case sdl.K_UP:
		return ControlEvent{Source: "sdl", Action: "seek", Value: 30}, true

	case sdl.K_DOWN:
		return ControlEvent{Source: "sdl", Action: "seek", Value: -30}, true

	case sdl.K_i, sdl.K_F7:
		return ControlEvent{Source: "sdl", Action: "info"}, true

	case sdl.K_QUOTE, sdl.K_POWER:
		return ControlEvent{Source: "sdl", Action: "poweroff"}, true

	default:
		return ControlEvent{}, false
	}
}

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
			dispatchControlEvent(safeClient, ev)
		case <-renderTicker.C:
			// Pump SDL events and render in the main thread
			for event := sdl.PollEvent(); event != nil; event = sdl.PollEvent() {
				switch e := event.(type) {
				case *sdl.QuitEvent:
					logger.Info("SDL window closed")
					return
				case *sdl.KeyboardEvent:
					if e.Type != sdl.KEYDOWN || e.Repeat != 0 {
						continue
					}

					keyCode := e.Keysym.Sym
					ev, ok := mapSDLKeyToControlEvent(keyCode)
					if !ok {
						logger.Debug(
							"Unhandled SDL key",
							"keycode", keyCode,
							"key_name", sdl.GetKeyName(keyCode),
							"scancode", e.Keysym.Scancode,
							"scancode_name", sdl.GetScancodeName(e.Keysym.Scancode),
						)
						continue
					}

					dispatchControlEvent(safeClient, ev)
				}
			}

			// Call render
			if sdlRenderer != nil && sdlRenderer.renderer != nil {
				sdlRenderer.render()
			}
		}
	}
}
