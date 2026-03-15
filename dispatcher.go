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
			mpdClient.Play(-1)

		case "pause":
			mpdClient.Pause(true)

		case "toggle":
			mpdClient.Pause(false)

		case "stop":
			mpdClient.Stop()

		case "next":
			mpdClient.Next()

		case "prev":
			mpdClient.Previous()

		case "seek":
			mpdClient.SeekCur(time.Duration(ev.Value)*time.Second, true)

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
