package main

import (
	"log/slog"
	"os/exec"
	"time"
)

// Dispatches commands to the MPD server based upon received events
func eventDispatcher(safeClient *SafeMPDClient, events <-chan ControlEvent) {
	for ev := range events {
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

		case "poweroff":
			logger.Warn("Powering off system...")
			err := exec.Command("systemctl", "poweroff").Run()
			if err != nil {
				logger.Error("Failed to power off", slog.Any("err", err))
			}

		default:
			logger.Warn("Unknown action", slog.Any("action", ev.Action))
		}
	}
}
