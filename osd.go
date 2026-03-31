package main

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"time"
)

type ProgressOSD struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
}

var (
	progressOSD *ProgressOSD
	progressMu  sync.Mutex
)

func osdMessage(text, font, colour string, delay, x, y, width int) {
	logger.Info("OSD message", "text", text, "x", x, "y", y, "width", width)

	cmd := exec.Command(
		"dzen2",
		"-ta", "c",
		"-fg", colour,
		"-x", fmt.Sprint(x),
		"-y", fmt.Sprint(y),
		"-w", fmt.Sprint(width),
		"-fn", font,
		"-p", fmt.Sprint(delay),
	)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		logger.Error("OSD pipe error", "err", err)
		return
	}

	err = cmd.Start()
	if err != nil {
		if os.IsNotExist(err) {
			logger.Error("OSD: dzen2 not installed")
		} else {
			logger.Error("OSD start error", "err", err)
		}
		return
	}

	io.WriteString(stdin, text+"\n")
	stdin.Close()

	// Reap the process to avoid zombie processes
	go cmd.Wait()
}

func showPlaybackIcon(state string) {
	switch state {
	case "play":
		osdMessage("▶", "Noto Sans Symbols 2-40", "white", 2, 370, 100, 60)

	case "pause":
		osdMessage("⏸", "Noto Sans Symbols 2-40", "white", 2, 370, 100, 60)

	case "stop":
		osdMessage("◼", "Noto Sans Symbols 2-40", "white", 2, 370, 100, 60)
	}
}

func showSongInfo(artist, album, title, track string) {
	if title == "" {
		return
	}
	osdMessage(artist, "DejaVu Sans-22", "#20c4bf", 6, 0, 0, 800)
	if album != "" {
		osdMessage(album, "DejaVu Sans-22:style=Oblique", "#20c4bf", 6, 0, 32, 800)
	}
	if track != "" {
		title = fmt.Sprintf("%s. %s", track, title)
	}
	osdMessage(title, "DejaVu Sans-22", "#20c4bf", 6, 0, 64, 800)
}

func startProgressUpdater(safeClient *SafeMPDClient, stop <-chan struct{}) {
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		for {
			select {

			case <-stop:
				return

			case <-ticker.C:
				mpdClient := safeClient.Get()
				if mpdClient == nil {
					logger.Warn("ProgressUpdater: MPD unavailable")
					time.Sleep(2 * time.Second)
					continue
				}

				status, err := (*mpdClient).Status()
				if err != nil {
					continue
				}

				if status["state"] != "play" {
					continue
				}

				elapsed, err1 := strconv.ParseFloat(status["elapsed"], 64)
				duration, err2 := strconv.ParseFloat(status["duration"], 64)

				if err1 != nil || err2 != nil {
					continue
				}
				renderProgress(elapsed, duration)
			}
		}
	}()
}

func startProgressOSD() {
	cmd := exec.Command(
		"dzen2",
		"-ta", "l",
		"-fg", "#20c4bf",
		"-bg", "black",
		"-x", "0",
		"-y", "470",
		"-w", "800",
		"-h", "20",
		"-fn", "DejaVu Sans Mono-14",
	)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		logger.Error("Progress OSD pipe error", slog.Any("err", err))
		return
	}

	err = cmd.Start()
	if err != nil {
		logger.Error("Progress OSD start error", slog.Any("err", err))
		return
	}

	progressMu.Lock()
	progressOSD = &ProgressOSD{
		cmd:   cmd,
		stdin: stdin,
	}
	progressMu.Unlock()
	logger.Info("Progress OSD started", "pid", cmd.Process.Pid)

	go func() {
		cmd.Wait()
		progressMu.Lock()
		progressOSD = nil
		progressMu.Unlock()
	}()
}

func progressPrint(text string) {
	progressMu.Lock()
	osd := progressOSD
	progressMu.Unlock()

	if osd == nil || osd.stdin == nil {
		return
	}
	_, err := io.WriteString(osd.stdin, text+"\n")
	if err != nil {
		logger.Warn("Progress OSD write failed", slog.Any("err", err))
	}
}

func renderProgress(elapsed, duration float64) {
	if duration <= 0 {
		return
	}
	const width = 76
	progress := int((elapsed / duration) * width)
	bar := ""

	for i := 0; i < width; i++ {
		if i < progress {
			bar += "▂"
		} else {
			bar += " "
		}
	}
	progressPrint(bar)
}

func stopProgressOSD() {
	progressMu.Lock()
	osd := progressOSD
	progressOSD = nil
	progressMu.Unlock()

	if osd == nil {
		return
	}
	osd.stdin.Close()
	osd.cmd.Process.Signal(syscall.SIGTERM)
}
