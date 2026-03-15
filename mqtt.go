package main

import (
	"encoding/json"
	"log/slog"
	"os"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/fhs/gompd/v2/mpd"
)

// Starts an MQTT listener for control messages
func startMQTT(events chan<- ControlEvent, server, prefix, user, pass string) mqtt.Client {
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
	client.Subscribe(prefix+"/control", 0, func(_ mqtt.Client, msg mqtt.Message) {
		payload := string(msg.Payload())
		events <- ControlEvent{Source: "mqtt", Action: payload}
	})

	return client
}

// Send an MQTT status message
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
