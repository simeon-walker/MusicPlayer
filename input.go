package main

import (
	"log/slog"
	"os"

	"github.com/holoplot/go-evdev"
)

// Starts an evdev listener for the remote control
func startEvDevHandler(devPath string, events chan<- ControlEvent, stopChan <-chan struct{}) {
	dev, err := evdev.Open(devPath)
	if err != nil {
		logger.Error("Failed to open input device", slog.Any("err", err))
		os.Exit(1)
	}
	logger.Info("Listening on input device", slog.Any("device", devPath))

	go func() {
		defer dev.Close() // Ensure device is closed on exit
		for {
			select {
			case <-stopChan: // Exit gracefully
				logger.Info("Input handler shutting down")
				return
			default:
			}

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

					case evdev.KEY_UP:
						events <- ControlEvent{Source: "input", Action: "seek", Value: 30}

					case evdev.KEY_DOWN:
						events <- ControlEvent{Source: "input", Action: "seek", Value: -30}

					case evdev.KEY_POWER:
						events <- ControlEvent{Source: "input", Action: "poweroff"}
					}
				}
			}
		}
	}()
}
