package main

import (
	"encoding/json"
	"flag"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/fhs/gompd/v2/mpd"

	evdev "github.com/holoplot/go-evdev"
)

// Define event types for our dispatcher
type ControlEvent struct {
	Source string // "mqtt" or "input"
	Action string // play, pause, next, prev, stop, seek+
	Value  int    // for seek seconds, etc.
}

type SafeMPDClient struct {
	client **mpd.Client
}

var logger = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

func (s *SafeMPDClient) Get() *mpd.Client {
	if s.client == nil || *s.client == nil {
		return nil
	}
	return *s.client
}

// ---- MQTT listener ----
func startMQTT(events chan<- ControlEvent, server, user, pass string) mqtt.Client {
	opts := mqtt.NewClientOptions().AddBroker(server)
	opts.SetClientID("mpd-controller")
	if user != "" {
		opts.SetUsername(user)
	}
	if pass != "" {
		opts.SetPassword(pass)
	}
	client := mqtt.NewClient(opts)

	if token := client.Connect(); token.Wait() && token.Error() != nil {
		logger.Error("MQTT connect error", slog.Any("err", token.Error()))
		os.Exit(1)
	}
	logger.Info("Connected to MQTT", slog.Any("server", server))

	// Subscribe for remote control
	client.Subscribe("home/media/control", 0, func(_ mqtt.Client, msg mqtt.Message) {
		payload := string(msg.Payload())
		events <- ControlEvent{Source: "mqtt", Action: payload}
	})

	return client
}

// ---- Input listener ----
func startInput(devPath string, events chan<- ControlEvent) {
	dev, err := evdev.Open(devPath)
	if err != nil {
		logger.Error("Failed to open input device", slog.Any("err", err))
		os.Exit(1)
	}
	logger.Info("Listening on input device", slog.Any("device", devPath))

	go func() {
		for {
			inputEvents, err := dev.ReadSlice(64)
			if err != nil {
				logger.Error("Error reading input", slog.Any("err", err))
				continue
			}
			for _, e := range inputEvents {
				if e.Type == evdev.EV_KEY && e.Value == 1 {
					switch e.Code {
					case evdev.KEY_PLAY:
						events <- ControlEvent{Source: "input", Action: "play"}
					case evdev.KEY_PAUSE:
						events <- ControlEvent{Source: "input", Action: "pause"}
					case evdev.KEY_PLAYPAUSE:
						events <- ControlEvent{Source: "input", Action: "toggle"}
					case evdev.KEY_STOPCD:
						events <- ControlEvent{Source: "input", Action: "stop"}
					case evdev.KEY_STOP:
						events <- ControlEvent{Source: "input", Action: "stop"}
					case evdev.KEY_NEXTSONG:
						events <- ControlEvent{Source: "input", Action: "next"}
					case evdev.KEY_PREVIOUSSONG:
						events <- ControlEvent{Source: "input", Action: "prev"}
					case evdev.KEY_FORWARD:
						events <- ControlEvent{Source: "input", Action: "seek", Value: 10}
					case evdev.KEY_FASTFORWARD:
						events <- ControlEvent{Source: "input", Action: "seek", Value: 10}
					case evdev.KEY_REWIND:
						events <- ControlEvent{Source: "input", Action: "seek", Value: -10}
					case evdev.KEY_POWER:
						events <- ControlEvent{Action: "poweroff"}
					}
				}
			}
		}
	}()
}

// ---- Dispatcher ----
func dispatcher(safeClient *SafeMPDClient, events <-chan ControlEvent) {
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

func startStatusPublisher(mpdAddr string, mpdClient **mpd.Client, mqttClient mqtt.Client, stopChan <-chan struct{}) {
	go func() {
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

			// Goroutine to log watcher errors
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

					// Fetch status and current song
					status, err := (*mpdClient).Status()
					if err != nil {
						logger.Warn("Status error. Reconnecting main client", slog.Any("err", err))
						(*mpdClient).Close()
						*mpdClient = nil
						continue
					}

					song, err := (*mpdClient).CurrentSong()
					if err != nil {
						logger.Error("CurrentSong error. Reconnecting main client", slog.Any("err", err))
						(*mpdClient).Close()
						*mpdClient = nil
						continue
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

// func getMPDClient(addr string, mpdClient **mpd.Client) (*mpd.Client, error) {
// 	if *mpdClient == nil {
// 		c, err := mpd.Dial("tcp", addr)
// 		if err != nil {
// 			return nil, err
// 		}
// 		*mpdClient = c
// 	}
// 	return *mpdClient, nil
// }

func sendMQTTStatus(mqttClient mqtt.Client, topic string, song mpd.Attrs, status mpd.Attrs) {

	// Build JSON payload
	payload := map[string]string{
		"state":  status["state"],
		"time":   status["time"],
		"title":  song["Title"],
		"artist": song["Artist"],
		"album":  song["Album"],
		"file":   song["file"],
	}

	data, err := json.Marshal(payload)
	if err != nil {
		logger.Error("JSON encode error", slog.Any("err", err))
		return
	}
	mqttClient.Publish(topic, 0, true, data)
}

func main() {
	// ---- Command-line flags ----
	mpdServer := flag.String("mpd", "localhost:6600", "MPD server address (host:port)")
	mqttServer := flag.String("mqtt-server", "tcp://localhost:1883", "MQTT server URI")
	mqttUser := flag.String("mqtt-user", "", "MQTT username (optional)")
	mqttPass := flag.String("mqtt-pass", "", "MQTT password (optional)")
	inputDevice := flag.String("input", "/dev/input/eventX", "Input device path (FLIRC)")
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

	// Start Input listener ----
	startInput(*inputDevice, events)

	// Start MPD status publisher ----
	stopChan := make(chan struct{})
	startStatusPublisher(*mpdServer, &mpdClient, mqttClient, stopChan)

	// Handle Ctrl+C / SIGTERM
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		logger.Warn("Shutting down...")
		close(stopChan)
		mqttClient.Disconnect(250)
		if mpdClient != nil {
			mpdClient.Close()
		}
		// dev.Close()
		os.Exit(0)
	}()

	// Run dispatcher (blocks main thread)
	dispatcher(safeClient, events)
}
