package main

import (
	"strconv"
	"time"
)

func startProgressUpdater(safeClient *SafeMPDClient, stop <-chan struct{}) {
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond) // Update progress 10 times per second for smoother display
		defer ticker.Stop()

		for {
			select {

			case <-stop:
				return

			case <-ticker.C:
				mpdClient := safeClient.Get()
				if mpdClient == nil {
					logger.Warn("ProgressUpdater: MPD unavailable")
					time.Sleep(2 * time.Second)
					continue
				}

				status, err := (*mpdClient).Status()
				if err != nil {
					continue
				}

				if status["state"] != "play" {
					continue
				}

				elapsed, err1 := strconv.ParseFloat(status["elapsed"], 64)
				duration, err2 := strconv.ParseFloat(status["duration"], 64)

				if err1 != nil || err2 != nil {
					continue
				}
				if sr := getSDLRenderer(); sr != nil {
					// Update SDL progress bar
					sr.UpdateProgress(elapsed, duration)
				}
			}
		}
	}()
}
