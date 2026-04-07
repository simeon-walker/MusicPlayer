package main

import (
	"bufio"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

var (
	cavaProcess       *exec.Cmd
	cavaStopRequested bool
	cavaMu            sync.Mutex
)

func startCava(configPath string) {
	cavaMu.Lock()
	if cavaProcess != nil {
		cavaMu.Unlock()
		logger.Info("Cava supervisor already running")
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

			logger.Info("Starting Cava", "config", configPath)
			// Run Cava directly (not in rxvt)
			// It will output bar heights as comma-separated numbers
			cmd := exec.Command("cava", "-p", configPath)

			// Get stdout pipe to read bar data
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				logger.Error("Failed to get Cava stdout", "err", err)
				time.Sleep(5 * time.Second)
				continue
			}

			err = cmd.Start()
			if err != nil {
				logger.Error("Failed to start Cava", "err", err)
				time.Sleep(5 * time.Second)
				continue
			}

			cavaMu.Lock()
			cavaProcess = cmd
			cavaMu.Unlock()
			logger.Info("Started Cava", "pid", cmd.Process.Pid)

			// Read bar data from Cava output
			scanner := bufio.NewScanner(stdout)
			for scanner.Scan() {
				line := scanner.Text()
				if line != "" {
					// logger.Debug("Cava output", "line", line[:min(80, len(line))])
					if sr := getSDLRenderer(); sr != nil {
						// Pass bar heights to SDL renderer
						sr.UpdateVisualizerBars(line)
					}
				}
			}

			if err := scanner.Err(); err != nil {
				logger.Warn("Cava read error", "err", err)
			}

			err = cmd.Wait()

			cavaMu.Lock()
			cavaProcess = nil
			shouldStop := cavaStopRequested
			cavaMu.Unlock()

			if shouldStop {
				logger.Info("Cava stopped")
				return
			}

			if err != nil {
				logger.Warn("Cava exited", "err", err)
			}

			logger.Info("Restarting Cava in 3s")
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
	logger.Info("Stopping Cava")

	err := proc.Process.Signal(syscall.SIGTERM)
	if err != nil {
		logger.Warn("Failed to signal Cava", "err", err)
	}
}
