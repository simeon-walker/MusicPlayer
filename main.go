package main

import (
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/joho/godotenv"
)

// Define event types for our dispatcher
type ControlEvent struct {
	Source string // "mqtt" or "input"
	Action string // play, pause, next, prev, stop, seek+
	Value  int    // for seek seconds, etc.
}

var logger = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

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
	mqttPrefixEnv, found := os.LookupEnv("MQTT_TOPIC")
	if !found {
		mqttPrefixEnv = "home/media"
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
	mqttPrefix := flag.String("mqtt-prefix", mqttPrefixEnv, "Prefix for MQTT /control and /status topics")
	mqttUser := flag.String("mqtt-user", mqttUserEnv, "MQTT username")
	mqttPass := flag.String("mqtt-pass", mqttPassEnv, "MQTT password")
	inputDevice := flag.String("input", inputDeviceEnv, "Input device path (FLIRC)")
	flag.Parse()

	events := make(chan ControlEvent, 10)

	// Connect main MPD client on demand
	safeClient := NewSafeMPDClient(*mpdServer)
	logger.Info("MPD server configured", slog.Any("server", *mpdServer))

	// Start MQTT
	mqttClient := startMQTT(events, *mqttServer, *mqttPrefix, *mqttUser, *mqttPass)

	stopChan := make(chan struct{})

	// Starts evdev input handler
	startEvDevHandler(*inputDevice, events, stopChan)

	// Start CAVA visualizer
	startCava()

	// Start MPD-MQTT status publisher
	mpdStatusWatcher(safeClient, mqttClient, *mqttPrefix, stopChan)

	// Show progress bar and start listener
	startProgressOSD()
	startProgressUpdater(safeClient, stopChan)

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

		safeClient.Close()

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
