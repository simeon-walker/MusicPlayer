package main

import (
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/fhs/gompd/v2/mpd"
	"github.com/joho/godotenv"
)

// Define event types for our dispatcher
type ControlEvent struct {
	Source string // "mqtt" or "input"
	Action string // play, pause, next, prev, stop, seek+
	Value  int    // for seek seconds, etc.
}

var logger = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

// Starts an MQTT publisher for MPD status messages
func startStatusPublisher(mpdAddr string, mpdClient **mpd.Client, mqttClient mqtt.Client, stopChan <-chan struct{}) {
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

					logger.Info("Changed subsystem", slog.Any("subsystem", subsystem))

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
						logger.Info("Playback state changed",
							"last_state", lastState,
							"state", state,
						)
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
						logger.Info("Current song changed",
							"last_file", lastFile,
							"file", file,
						)
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

func main() {
	// Load .env file if it exists
	loadDotEnv()

	// Process environment variables with defaults
	mpdServerEnv, found := os.LookupEnv("MPD_SERVER")
	if !found {
		mpdServerEnv = "localhost:6600"
	}
	mqttServerEnv, found := os.LookupEnv("MQTT_SERVER")
	if !found {
		mqttServerEnv = "tcp://localhost:1883"
	}
	mqttUserEnv, _ := os.LookupEnv("MQTT_USER")
	mqttPassEnv, _ := os.LookupEnv("MQTT_PASS")
	inputDeviceEnv, found := os.LookupEnv("INPUT_DEVICE")
	if !found {
		inputDeviceEnv = "/dev/input/eventX"
	}

	// ---- Command-line flags ----
	mpdServer := flag.String("mpd", mpdServerEnv, "MPD server address (host:port)")
	mqttServer := flag.String("mqtt-server", mqttServerEnv, "MQTT server URI")
	mqttUser := flag.String("mqtt-user", mqttUserEnv, "MQTT username")
	mqttPass := flag.String("mqtt-pass", mqttPassEnv, "MQTT password")
	inputDevice := flag.String("input", inputDeviceEnv, "Input device path (FLIRC)")
	flag.Parse()

	events := make(chan ControlEvent, 10)

	// Connect main MPD client on demand
	var mpdClient *mpd.Client
	var err error
	mpdClient, err = mpd.Dial("tcp", *mpdServer)
	if err != nil {
		logger.Error("Initial MPD connect failed, will retry in watcher:", slog.Any("err", err))
		mpdClient = nil
	} else {
		defer mpdClient.Close()
		logger.Info("Connected to MPD", slog.Any("server", *mpdServer))
	}
	safeClient := &SafeMPDClient{client: &mpdClient}

	// Start MQTT
	mqttClient := startMQTT(events, *mqttServer, *mqttUser, *mqttPass)

	// Starts evdev input handler
	startEvDevHandler(*inputDevice, events)

	// Start CAVA visualizer
	startCava()

	// Start MPD-MQTT status publisher
	stopChan := make(chan struct{})
	startStatusPublisher(*mpdServer, &mpdClient, mqttClient, stopChan)

	// Show progress bar and start listener
	startProgressOSD()
	startProgressUpdater(&mpdClient, stopChan)

	// Handle Ctrl+C / SIGTERM
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		logger.Warn("Shutting down...")

		stopProgressOSD()
		stopCava()

		close(stopChan)
		mqttClient.Disconnect(250)

		if mpdClient != nil {
			mpdClient.Close()
		}

		os.Exit(0)
	}()

	// Run dispatcher (blocks main thread)
	eventDispatcher(safeClient, events)
}

func loadDotEnv() {
	// Attempt to load .env file if it exists
	if _, err := os.Stat(".env"); err == nil {
		logger.Info("Loading .env file")
		err = godotenv.Load()
		if err != nil {
			logger.Error("Error loading .env file", slog.Any("err", err))
		}
	} else {
		logger.Info(".env file not found, skipping")
	}
}
