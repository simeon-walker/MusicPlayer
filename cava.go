package main

import (
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

var (
	cavaProcess       *exec.Cmd
	cavaStopRequested bool
	cavaMu            sync.Mutex // Add this
)

var cavaCmd = []string{
	"rxvt", "-fn", "5x8",
	"-geometry", "160x58+0+0",
	"-bg", "black", "-fg", "white", "+sb",
	"-e", "cava",
	"-p", os.ExpandEnv("$HOME/.config/cava/config"),
}

func startCava() {
	cavaMu.Lock()
	if cavaProcess != nil {
		cavaMu.Unlock()
		logger.Info("CAVA supervisor already running")
		return
	}
	cavaMu.Unlock()

	go func() {
		for {
			cavaMu.Lock()
			if cavaStopRequested {
				cavaMu.Unlock()
				return
			}
			cavaMu.Unlock()

			cmd := exec.Command(cavaCmd[0], cavaCmd[1:]...)
			err := cmd.Start()
			if err != nil {
				logger.Error("Failed to start CAVA", "err", err)
				time.Sleep(5 * time.Second)
				continue
			}

			cavaMu.Lock() // Update safely
			cavaProcess = cmd
			cavaMu.Unlock()
			logger.Info("Started CAVA", "pid", cmd.Process.Pid)

			err = cmd.Wait()

			cavaMu.Lock()
			cavaProcess = nil
			shouldStop := cavaStopRequested
			cavaMu.Unlock()

			if shouldStop {
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
	cavaMu.Lock()
	cavaStopRequested = true
	proc := cavaProcess
	cavaMu.Unlock()

	if proc == nil || proc.Process == nil {
		return
	}
	logger.Info("Stopping CAVA")

	err := proc.Process.Signal(syscall.SIGTERM)
	if err != nil {
		logger.Warn("Failed to signal CAVA", "err", err)
	}
}
