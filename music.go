package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/fhs/gompd/v2/mpd"

	// evdev "github.com/gvalkov/golang-evdev"
	evdev "github.com/holoplot/go-evdev"
)

// Define event types for our dispatcher
type ControlEvent struct {
	Source string // "mqtt" or "input"
	Action string // play, pause, next, prev, stop, seek+
	Value  int    // for seek seconds, etc.
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
		log.Fatalf("MQTT connect error: %v", token.Error())
	}
	fmt.Println("Connected to MQTT")

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
		log.Fatalf("Failed to open input device: %v", err)
	}
	fmt.Printf("Listening on input device: %s\n", devPath)

	go func() {
		for {
			inputEvents, err := dev.ReadSlice(9)
			if err != nil {
				log.Printf("Error reading input: %v", err)
				continue
			}
			for _, e := range inputEvents {
				if e.Type == evdev.EV_KEY && e.Value == 1 {
					switch e.Code {
					case evdev.KEY_NEXTSONG:
						events <- ControlEvent{Source: "input", Action: "next"}
					case evdev.KEY_PREVIOUSSONG:
						events <- ControlEvent{Source: "input", Action: "prev"}
					case evdev.KEY_PLAYPAUSE:
						events <- ControlEvent{Source: "input", Action: "toggle"}
					case evdev.KEY_STOPCD:
						events <- ControlEvent{Source: "input", Action: "stop"}
					case evdev.KEY_REWIND:
						events <- ControlEvent{Source: "input", Action: "seek", Value: -10}
					case evdev.KEY_FORWARD:
						events <- ControlEvent{Source: "input", Action: "seek", Value: 10}
					}
				}
			}
		}
	}()
}

// ---- Dispatcher ----
func dispatcher(mpdClient *mpd.Client, events <-chan ControlEvent) {
	for ev := range events {
		fmt.Printf("Dispatch: %+v\n", ev)

		switch ev.Action {
		case "play":
			mpdClient.Play(-1)
		case "pause":
			mpdClient.Pause(true)
		case "toggle":
			mpdClient.Pause(false) // or TogglePause if you prefer
		case "next":
			mpdClient.Next()
		case "prev":
			mpdClient.Previous()
		case "stop":
			mpdClient.Stop()
		case "seek":
			mpdClient.SeekCur(time.Duration(ev.Value)*time.Second, true)
		default:
			fmt.Printf("Unknown action: %s\n", ev.Action)
		}
	}
}

// ---- MPD status publisher ----
func startStatusPublisher(mpdClient *mpd.Client, mqttClient mqtt.Client) {
	go func() {
		// idle blocks until something changes in MPD
		idleClient, err := mpd.Dial("tcp", "localhost:6600")
		if err != nil {
			log.Fatalf("Failed to open MPD idle connection: %v", err)
		}
		defer idleClient.Close()

		for {
			// Wait for player or playlist changes
			_, err := idleClient.Command("idle player playlist").Strings("")
			if err != nil {
				log.Printf("Idle error: %v", err)
				time.Sleep(2 * time.Second)
				continue
			}

			// Fetch status + song info
			status, _ := mpdClient.Status()
			song, _ := mpdClient.CurrentSong()

			// Build JSON payload
			payload := map[string]string{
				"state":  status["state"], // play, pause, stop
				"title":  song["Title"],
				"artist": song["Artist"],
				"album":  song["Album"],
				"file":   song["file"],   // useful for streams or when tags are missing
				"time":   status["time"], // "elapsed:total" seconds
			}

			// Encode to JSON
			data, err := json.Marshal(payload)
			if err != nil {
				log.Printf("JSON encode error: %v", err)
				continue
			}

			// Publish once per change
			mqttClient.Publish("home/media/status", 0, true, data)
		}
	}()
}

func main() {
	// ---- Command-line flags ----
	mqttServer := flag.String("mqtt-server", "tcp://localhost:1883", "MQTT server URI")
	mqttUser := flag.String("mqtt-user", "", "MQTT username (optional)")
	mqttPass := flag.String("mqtt-pass", "", "MQTT password (optional)")
	inputDevice := flag.String("input", "/dev/input/eventX", "Input device path (FLIRC)")

	flag.Parse()

	// ---- 1. Connect to MPD ----
	mpdClient, err := mpd.Dial("tcp", "localhost:6600")
	if err != nil {
		log.Fatalf("Failed to connect to MPD: %v", err)
	}
	defer mpdClient.Close()
	fmt.Println("Connected to MPD")

	// ---- 2. Start listeners ----
	events := make(chan ControlEvent, 10)

	// ---- 3. Start MQTT ----
	mqttClient := startMQTT(events, *mqttServer, *mqttUser, *mqttPass)

	// ---- 4. Start Input listener ----
	startInput(*inputDevice, events) // replace with FLIRC path

	// ---- 5. Start MPD status publisher ----
	startStatusPublisher(mpdClient, mqttClient)

	// ---- 6. Run dispatcher ----
	dispatcher(mpdClient, events)
}
