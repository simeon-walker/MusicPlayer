package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/fhs/gompd/v2/mpd"
)

// Starts an MQTT listener for control messages
func startMQTT(events chan<- ControlEvent, server, prefix, user, pass string) mqtt.Client {
	controlHandler := mqttControlHandler(events)

	opts := mqtt.NewClientOptions().AddBroker(server)
	opts.SetCleanSession(true)
	opts.SetClientID(fmt.Sprintf("mpd-controller-%d", os.Getpid()))
	opts.SetAutoReconnect(true)
	opts.SetOnConnectHandler(func(c mqtt.Client) {
		c.Subscribe(prefix+"/control", 0, controlHandler)
	})
	opts.SetConnectionLostHandler(func(c mqtt.Client, err error) {
		logger.Warn("MQTT connection lost", slog.Any("err", err))
	})

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
	token := client.Subscribe(prefix+"/control", 0, controlHandler)

	if !token.WaitTimeout(5 * time.Second) {
		logger.Error("MQTT subscribe timeout")
		os.Exit(1)
	}

	if token.Error() != nil {
		logger.Error("MQTT subscribe failed", slog.Any("err", token.Error()))
		os.Exit(1)
	}

	logger.Info("Subscribed to MQTT topic", slog.Any("topic", prefix+"/control"))

	return client
}

func mqttControlHandler(events chan<- ControlEvent) mqtt.MessageHandler {
	return func(_ mqtt.Client, msg mqtt.Message) {
		payload := string(msg.Payload())
		logger.Info("control", "payload", payload)
		action := payload
		value := 0
		parts := strings.Split(payload, ":")
		if len(parts) == 2 {
			action = parts[0]
			if v, err := strconv.Atoi(parts[1]); err == nil {
				value = v
			}
		}
		events <- ControlEvent{Source: "mqtt", Action: action, Value: value}
	}
}

// Send an MQTT status message
func sendMQTTStatus(mqttClient mqtt.Client, topic string, song mpd.Attrs, status mpd.Attrs, playlistAdded, playlistRemoved int) {

	// Build JSON payload
	payload := map[string]interface{}{
		"state":            status["state"],
		"time":             status["time"],
		"title":            song["Title"],
		"artist":           song["Artist"],
		"album":            song["Album"],
		"file":             song["file"],
		"playlist_length":  status["playlistlength"],
		"playlist_added":   playlistAdded,
		"playlist_removed": playlistRemoved,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		logger.Error("JSON encode error", slog.Any("err", err))
		return
	}
	mqttClient.Publish(topic, 0, true, data)
}
