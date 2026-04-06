package main

import (
	"log/slog"
	"strconv"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/fhs/gompd/v2/mpd"
)

type SafeMPDClient struct {
	addr   string
	client *mpd.Client
	mu     sync.Mutex
}

func NewSafeMPDClient(addr string) *SafeMPDClient {
	return &SafeMPDClient{
		addr: addr,
	}
}

func (s *SafeMPDClient) Get() *mpd.Client {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.client != nil {
		// Check if connection is still alive
		if err := s.client.Ping(); err != nil {
			logger.Warn("MPD connection lost, reconnecting", "err", err)
			s.client.Close()
			s.client = nil
		} else {
			return s.client
		}
	}

	// Connect or reconnect
	c, err := mpd.Dial("tcp", s.addr)
	if err != nil {
		logger.Error("MPD connect failed", slog.Any("err", err))
		return nil
	}

	logger.Info("Connected to MPD")
	s.client = c
	return s.client
}

func (s *SafeMPDClient) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.client == nil {
		return
	}
	if s.client != nil {
		s.client.Close()
	}
	s.client = nil
}

// Starts an MQTT publisher for MPD status messages
func mpdStatusWatcher(safeClient *SafeMPDClient, mqttClient mqtt.Client, mqttPrefix string, stopChan <-chan struct{}) {

	go func() {
		var lastState string
		var lastTitle string
		var lastFile string
		lastPlaylistLength := -1

		for {
			select {
			case <-stopChan:
				return
			default:
			}

			// Start or restart the watcher
			w, err := mpd.NewWatcher("tcp", safeClient.addr, "", "player playlist")
			if err != nil {
				logger.Error("Watcher start failed", slog.Any("err", err))
				time.Sleep(2 * time.Second)
				continue
			}
			logger.Info("MPD watcher started")

			// Capture a baseline queue length so the first playlist event can emit a delta.
			if mpdClient := safeClient.Get(); mpdClient != nil {
				if status, err := mpdClient.Status(); err != nil {
					logger.Warn("Failed to read initial MPD status", slog.Any("err", err))
				} else {
					playlistLength, err := strconv.Atoi(status["playlistlength"])
					if err != nil {
						logger.Warn("Invalid initial playlistlength", "value", status["playlistlength"], slog.Any("err", err))
					} else {
						lastPlaylistLength = playlistLength
						logger.Debug("Initialized playlist length baseline", "playlist_length", playlistLength)
					}
				}
			}

			ticker := time.NewTicker(45 * time.Second)

		watcherLoop:
			for {
				select {

				case <-stopChan:
					ticker.Stop()
					w.Close()
					return

				case err := <-w.Error:
					logger.Error("Watcher error", slog.Any("err", err))
					safeClient.Close()
					break watcherLoop

				case subsystem := <-w.Event:
					logger.Info("Subsystem change", slog.Any("subsystem", subsystem))
					playlistAdded := 0
					playlistRemoved := 0

					// Ensure main mpdClient is connected
					mpdClient := safeClient.Get()
					if mpdClient == nil {
						continue
					}

					// Fetch status
					status, err := mpdClient.Status()
					if err != nil {
						logger.Warn("Status failed", slog.Any("err", err))
						safeClient.Close()
						continue
					}

					if subsystem == "playlist" {
						playlistLength, err := strconv.Atoi(status["playlistlength"])
						if err != nil {
							logger.Warn("Invalid playlistlength", "value", status["playlistlength"], slog.Any("err", err))
						} else {
							if lastPlaylistLength >= 0 && playlistLength != lastPlaylistLength {
								delta := playlistLength - lastPlaylistLength
								if delta > 0 {
									playlistAdded = delta
									logger.Info("Playlist items added", "count", delta, "playlist_length", playlistLength)
								} else {
									playlistRemoved = -delta
									logger.Info("Playlist items removed", "count", -delta, "playlist_length", playlistLength)
								}
							}
							lastPlaylistLength = playlistLength
						}
					}

					// Playback state change
					state := status["state"]
					if state != lastState {
						logger.Info("Playback state changed", "last_state", lastState, "state", state)
						showPlaybackIcon(state)
						if state == "stop" {
							// Clear progress bar on stop
							UpdateProgress(0, 0)
						}
						lastState = state
					}

					// Fetch current song

					song, err := mpdClient.CurrentSong()
					if err != nil {
						logger.Warn("CurrentSong failed", slog.Any("err", err))
						safeClient.Close()
						continue
					}

					title := song["Title"]
					file := song["file"]

					// Song changed
					if file != lastFile {
						ShowSongInfo(song["Artist"], song["Album"], title, song["Track"])
						lastFile = file
						lastTitle = title
					}

					// Title changed (covers streams)
					if title != "" && title != lastTitle {
						ShowSongInfo(song["Artist"], song["Album"], title, song["Track"])
						lastTitle = title
					}

					audio := status["audio"]
					if audio != "" {
						logger.Info("Audio format", "audio", audio)
					}

					sendMQTTStatus(mqttClient, mqttPrefix+"/status", song, status, playlistAdded, playlistRemoved)

				case <-ticker.C:
					// Periodic keepalive
					mpdClient := safeClient.Get()
					if mpdClient == nil {
						continue
					}

					_, err := mpdClient.Status()
					if err != nil {
						logger.Warn("Keepalive failed", slog.Any("err", err))
						safeClient.Close()
					}
				}
			}

			// Close watcher and back off before reconnect
			ticker.Stop()
			w.Close()

			logger.Info("Reconnecting MPD watcher in 2s...")
			time.Sleep(2 * time.Second)
		}
	}()
}
