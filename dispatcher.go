package main

import (
	"log/slog"
	"os/exec"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/fhs/gompd/v2/mpd"
)

type SafeMPDClient struct {
	client **mpd.Client
}

func (s *SafeMPDClient) Get() *mpd.Client {
	if s.client == nil || *s.client == nil {
		return nil
	}
	return *s.client
}

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

// Starts an MQTT publisher for MPD status messages
func mpdStatusWatcher(mpdAddr string, mpdClient **mpd.Client, mqttClient mqtt.Client, stopChan <-chan struct{}) {
	go func() {
		var lastState string
		var lastTitle string
		var lastFile string
		ticker := time.NewTicker(45 * time.Second) // keepalive interval
		defer ticker.Stop()

		for {
			select {
			case <-stopChan:
				return
			default:
			}

			// Start or restart the watcher
			w, err := mpd.NewWatcher("tcp", mpdAddr, "", "player playlist")
			if err != nil {
				logger.Error("Failed to start MPD watcher. Retrying in 2s...", slog.Any("err", err))
				time.Sleep(2 * time.Second)
				continue
			}
			logger.Info("MPD watcher started")

			// Go routine to log watcher errors
			go func() {
				for err := range w.Error {
					logger.Error("Watcher error", slog.Any("err", err))
				}
			}()

			// Event loop
			reconnect := false
			for !reconnect {
				select {
				case <-stopChan:
					w.Close()
					return
				case subsystem, ok := <-w.Event:
					if !ok {
						logger.Warn("Watcher channel closed, reconnecting...")
						reconnect = true
						break
					}
					logger.Info("Subsystem change", slog.Any("subsystem", subsystem))

					// Ensure main mpdClient is connected
					if *mpdClient == nil {
						c, err := mpd.Dial("tcp", mpdAddr)
						if err != nil {
							logger.Error("Failed to connect main MPD client", slog.Any("err", err))
							continue
						}
						*mpdClient = c
					}

					// Fetch status
					status, err := (*mpdClient).Status()
					if err != nil {
						logger.Warn("Status error. Reconnecting main client", slog.Any("err", err))
						(*mpdClient).Close()
						*mpdClient = nil
						continue
					}
					// Playback state change
					state := status["state"]
					if state != lastState {
						logger.Info("Playback state changed", "last_state", lastState, "state", state)
						showPlaybackIcon(state)
						if state == "stop" {
							progressPrint("")
						}
						lastState = state
					}

					// Fetch current song
					song, err := (*mpdClient).CurrentSong()
					if err != nil {
						logger.Error("CurrentSong error. Reconnecting main client", slog.Any("err", err))
						(*mpdClient).Close()
						*mpdClient = nil
						continue
					}
					title := song["Title"]
					artist := song["Artist"]
					album := song["Album"]
					file := song["file"]

					// Song changed
					if file != lastFile {
						logger.Info("Current song changed", "last_file", lastFile, "file", file)
						showSongInfo(artist, title, album)
						lastFile = file
						lastTitle = title
					}
					// Title changed (covers streams)
					if title != "" && title != lastTitle {
						showSongInfo(artist, title, album)
						lastTitle = title
					}

					sendMQTTStatus(mqttClient, "home/media/status", song, status)

				case <-ticker.C:
					// Periodic keepalive
					if *mpdClient != nil {
						_, err := (*mpdClient).Status()
						if err != nil {
							logger.Error("Keepalive failed, reconnecting main client:", slog.Any("err", err))
							(*mpdClient).Close()
							*mpdClient = nil
						}
					}
				}
			}

			// Close watcher and back off before reconnect
			w.Close()
			logger.Info("Reconnecting MPD watcher in 2s...")
			time.Sleep(2 * time.Second)
		}
	}()
}
