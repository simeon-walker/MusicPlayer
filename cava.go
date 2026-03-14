package main

import (
	"os"
	"os/exec"
	"syscall"
	"time"
)

var cavaProcess *exec.Cmd
var cavaStopRequested bool

var cavaCmd = []string{
	"rxvt", "-fn", "5x8",
	"-geometry", "160x58+0+0",
	"-bg", "black", "-fg", "white", "+sb",
	"-e", "cava",
	"-p", os.ExpandEnv("$HOME/.config/cava/config"),
}

func startCava() {
	if cavaProcess != nil {
		logger.Info("CAVA supervisor already running")
		return
	}

	go func() {
		for {
			if cavaStopRequested {
				return
			}

			cmd := exec.Command(cavaCmd[0], cavaCmd[1:]...)
			err := cmd.Start()
			if err != nil {
				logger.Error("Failed to start CAVA", "err", err)
				time.Sleep(5 * time.Second)
				continue
			}
			cavaProcess = cmd
			logger.Info("Started CAVA", "pid", cmd.Process.Pid)

			err = cmd.Wait()
			cavaProcess = nil
			if cavaStopRequested {
				logger.Info("CAVA stopped")
				return
			}
			if err != nil {
				logger.Warn("CAVA exited unexpectedly", "err", err)
			} else {
				logger.Warn("CAVA exited")
			}

			logger.Info("Restarting CAVA in 3s")
			time.Sleep(3 * time.Second)
		}
	}()
}

func stopCava() {
	cavaStopRequested = true
	if cavaProcess == nil || cavaProcess.Process == nil {
		return
	}
	logger.Info("Stopping CAVA")

	err := cavaProcess.Process.Signal(syscall.SIGTERM)
	if err != nil {
		logger.Warn("Failed to signal CAVA", "err", err)
	}
}
