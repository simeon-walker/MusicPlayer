package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/veandco/go-sdl2/sdl"
	"github.com/veandco/go-sdl2/ttf"
)

const (
	WindowWidth       = 800
	WindowHeight      = 480
	ProgressBarHeight = 50
	VisualizerHeight  = WindowHeight - ProgressBarHeight

	// Font paths - adjust for small displays like Raspberry Pi touchscreen
	DefaultFontPath = "/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf"
	IconFontPath    = "/usr/share/fonts/truetype/noto/NotoSansSymbols2-Regular.ttf" // Use a font with better symbols if needed

	DefaultFontSize         = 20
	LargeFontSize           = 24
	IconFontSize            = 24
	SongInfoDisplayDuration = 5 * time.Second
)

type SDLRenderer struct {
	window      *sdl.Window
	renderer    *sdl.Renderer
	defaultFont *ttf.Font
	largeFont   *ttf.Font
	iconFont    *ttf.Font

	// Visualizer data
	barHeights []int
	barMu      sync.Mutex

	// Display state
	displayMu      sync.Mutex
	songInfo       SongDisplay
	songInfoExpiry time.Time
	progress       ProgressDisplay
	playState      string
}

type SongDisplay struct {
	Artist string
	Album  string
	Title  string
	Track  string
}

type ProgressDisplay struct {
	Elapsed  float64
	Duration float64
}

var (
	sdlRenderer *SDLRenderer
	sdlMu       sync.Mutex
)

// InitSDL initializes SDL and creates the window
func InitSDL() error {
	sdlMu.Lock()
	defer sdlMu.Unlock()

	if sdlRenderer != nil {
		return nil // Already initialized
	}

	logger.Debug("Initializing SDL2")

	// Log environment info
	logger.Debug("SDL Environment",
		"DISPLAY", os.Getenv("DISPLAY"),
		"WAYLAND_DISPLAY", os.Getenv("WAYLAND_DISPLAY"),
		"SDL_VIDEODRIVER", os.Getenv("SDL_VIDEODRIVER"),
	)

	// Initialize SDL
	if err := sdl.Init(sdl.INIT_VIDEO); err != nil {
		return fmt.Errorf("SDL init failed: %v", err)
	}
	logger.Debug("SDL video subsystem initialized")

	// Initialize TTF
	if err := ttf.Init(); err != nil {
		sdl.Quit()
		return fmt.Errorf("TTF init failed: %v", err)
	}

	// Create window
	logger.Debug("Creating window", "width", WindowWidth, "height", WindowHeight)
	window, err := sdl.CreateWindow(
		"Music Player",
		sdl.WINDOWPOS_CENTERED,
		sdl.WINDOWPOS_CENTERED,
		WindowWidth,
		WindowHeight,
		sdl.WINDOW_SHOWN|sdl.WINDOW_ALWAYS_ON_TOP,
	)
	if err != nil {
		ttf.Quit()
		sdl.Quit()
		return fmt.Errorf("window creation failed: %v", err)
	}
	logger.Info("Window created successfully")

	// Create renderer - try software renderer first in containerized environments
	logger.Debug("Creating renderer")

	// Try software renderer first for better compatibility
	renderer, err := sdl.CreateRenderer(window, -1, sdl.RENDERER_ACCELERATED)
	if err != nil {
		logger.Debug("Accelerated renderer failed, trying software", "err", err)
		renderer, err = sdl.CreateRenderer(window, -1, sdl.RENDERER_SOFTWARE)
		if err != nil {
			window.Destroy()
			ttf.Quit()
			sdl.Quit()
			return fmt.Errorf("renderer creation failed: %v", err)
		}
		logger.Info("Renderer created (software)")
	} else {
		logger.Info("Renderer created (accelerated)")
	}

	renderer.SetDrawBlendMode(sdl.BLENDMODE_BLEND)
	renderer.SetDrawColor(0, 0, 0, 255)
	renderer.Clear()
	renderer.Present()

	// Load fonts
	defaultFontPath := DefaultFontPath
	defaultfont, err := ttf.OpenFont(defaultFontPath, DefaultFontSize)
	if err != nil {
		logger.Warn("Could not load font", "path", defaultFontPath, "err", err)
		defaultFontPath = "/usr/share/fonts/truetype/liberation/LiberationSans-Regular.ttf"
		defaultfont, err = ttf.OpenFont(defaultFontPath, DefaultFontSize)
		if err != nil {
			logger.Warn("Could not load fallback font", "err", err)
		}
	}

	largeFont, err := ttf.OpenFont(defaultFontPath, LargeFontSize)
	if err != nil {
		logger.Warn("Could not load big font", "err", err)
	}

	iconFont, err := ttf.OpenFont(IconFontPath, IconFontSize)
	if err != nil {
		logger.Warn("Could not load icon font", "path", IconFontPath, "err", err)
		iconFont = defaultfont // Fallback to default font
	}

	sdlRenderer = &SDLRenderer{
		window:      window,
		renderer:    renderer,
		defaultFont: defaultfont,
		largeFont:   largeFont,
		iconFont:    iconFont,
		barHeights:  make([]int, 0),
		playState:   "stop",
	}

	logger.Info("SDL initialized", "size", fmt.Sprintf("%dx%d", WindowWidth, WindowHeight))
	return nil
}

// ShutdownSDL cleans up SDL resources
func ShutdownSDL() {
	sdlMu.Lock()
	defer sdlMu.Unlock()

	if sdlRenderer == nil {
		return
	}

	if sdlRenderer.largeFont != nil {
		sdlRenderer.largeFont.Close()
	}
	if sdlRenderer.iconFont != nil {
		sdlRenderer.iconFont.Close()
	}
	if sdlRenderer.defaultFont != nil {
		sdlRenderer.defaultFont.Close()
	}
	if sdlRenderer.renderer != nil {
		sdlRenderer.renderer.Destroy()
	}
	if sdlRenderer.window != nil {
		sdlRenderer.window.Destroy()
	}

	ttf.Quit()
	sdl.Quit()

	sdlRenderer = nil
	logger.Info("SDL shutdown complete")
}

// UpdateVisualizerBars updates the bar heights from Cava
// Input: semicolon-separated numbers like "10;20;15;25;30;..."
func UpdateVisualizerBars(barData string) {
	sdlMu.Lock()
	sr := sdlRenderer
	sdlMu.Unlock()
	if sr == nil {
		return
	}

	parts := strings.Split(strings.TrimSpace(barData), ";")
	heights := make([]int, 0, len(parts))

	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		h, err := strconv.Atoi(p)
		if err != nil {
			logger.Debug("Failed to parse bar height", "value", p, "err", err)
			continue
		}
		heights = append(heights, h)
	}

	if len(heights) == 0 {
		logger.Debug("No valid bar heights parsed", "raw", barData[:min(50, len(barData))])
		return
	}

	logger.Debug("Updated bars", "count", len(heights), "first_bar", heights[0], "max_bar", max(heights))

	sdlRenderer.barMu.Lock()
	sdlRenderer.barHeights = heights
	sdlRenderer.barMu.Unlock()
}

func max(heights []int) int {
	if len(heights) == 0 {
		return 0
	}
	m := heights[0]
	for _, h := range heights[1:] {
		if h > m {
			m = h
		}
	}
	return m
}

// UpdateProgress updates the progress display
func UpdateProgress(elapsed, duration float64) {
	sdlMu.Lock()
	sr := sdlRenderer
	sdlMu.Unlock()
	if sr == nil {
		return
	}

	sr.displayMu.Lock()
	sr.progress.Elapsed = elapsed
	sr.progress.Duration = duration
	sr.displayMu.Unlock()
}

// ShowSongInfo displays song information
func ShowSongInfo(artist, album, title, track string) {
	if title == "" {
		return
	}
	sdlMu.Lock()
	sr := sdlRenderer
	sdlMu.Unlock()
	if sr == nil {
		return
	}

	sr.displayMu.Lock()
	sr.songInfo = SongDisplay{
		Artist: artist,
		Album:  album,
		Title:  title,
		Track:  track,
	}
	sr.songInfoExpiry = time.Now().Add(SongInfoDisplayDuration)
	sr.displayMu.Unlock()
}

func RefreshSongInfo() {
	sdlMu.Lock()
	sr := sdlRenderer
	sdlMu.Unlock()
	if sr == nil {
		return
	}

	sr.displayMu.Lock()
	if sr.songInfo.Title != "" {
		sr.songInfoExpiry = time.Now().Add(SongInfoDisplayDuration)
	}
	sr.displayMu.Unlock()
}

// UpdatePlayState updates the playback state icon
func UpdatePlayState(state string) {
	sdlMu.Lock()
	sr := sdlRenderer
	sdlMu.Unlock()
	if sr == nil {
		return
	}

	sr.displayMu.Lock()
	sr.playState = state
	sr.displayMu.Unlock()
}

// render draws the current frame
func (sr *SDLRenderer) render() {
	if sr == nil || sr.renderer == nil {
		return
	}

	// Clear background
	sr.renderer.SetDrawColor(0, 0, 0, 255)
	sr.renderer.Clear()

	// Draw dynamic content
	sr.drawVisualizer()
	sr.drawProgressBar()
	sr.drawSongInfoOverlay()

	// Finalize frame
	sr.renderer.Present()
}

// drawVisualizer draws the Cava bar graph
func (sr *SDLRenderer) drawVisualizer() {
	sr.barMu.Lock()
	heights := make([]int, len(sr.barHeights))
	copy(heights, sr.barHeights)
	numBars := len(heights)
	sr.barMu.Unlock()

	if numBars == 0 {
		// Draw a bright test pattern so the app is clearly visible when no data has arrived.
		sr.renderer.SetDrawColor(20, 80, 120, 255)
		sr.renderer.FillRect(&sdl.Rect{X: 0, Y: 0, W: WindowWidth, H: VisualizerHeight})

		sr.renderer.SetDrawColor(255, 255, 255, 255)
		sr.renderer.DrawLine(0, 0, WindowWidth, VisualizerHeight)
		sr.renderer.DrawLine(0, VisualizerHeight, WindowWidth, 0)

		// Draw center crosshairs
		sr.renderer.SetDrawColor(255, 255, 0, 255)
		sr.renderer.DrawLine(WindowWidth/2, 0, WindowWidth/2, VisualizerHeight)
		sr.renderer.DrawLine(0, VisualizerHeight/2, WindowWidth, VisualizerHeight/2)

		logger.Debug("Rendering test pattern - no bar data yet")
		return
	}

	barWidth := WindowWidth / numBars
	logger.Debug("Drawing visualizer", "bars", numBars, "bar_width", barWidth)

	// Always draw grid/baseline even if all bars are zero
	sr.renderer.SetDrawColor(30, 50, 50, 128)
	for i := 0; i < numBars; i++ {
		x := int32(i * barWidth)
		sr.renderer.DrawLine(x, int32(VisualizerHeight)-2, x, int32(VisualizerHeight)+2)
	}

	// Draw bars
	drawnBars := 0
	for i, h := range heights {
		// Ensure height is within valid range (assume Cava outputs 0-255)
		if h < 0 {
			h = 0
		}
		if h > 255 {
			h = 255
		}

		// Scale height to fit in visualizer area
		scaledHeight := (h * VisualizerHeight) / 255
		if scaledHeight > VisualizerHeight {
			scaledHeight = VisualizerHeight
		}

		// Position: bottom-aligned in visualizer area
		x := int32(i * barWidth)
		y := int32(VisualizerHeight - scaledHeight)
		w := int32(barWidth - 1) // Small gap between bars
		barH := int32(scaledHeight)

		if barH > 0 {
			drawnBars++
			// Draw gradient-colored bar (teal to cyan)
			// Color intensity based on height
			intensity := uint8((barH * 200) / int32(VisualizerHeight))
			r := uint8(32 + intensity/4)
			g := uint8(200)
			b := uint8(191)

			// Fill the bar with slightly darker outline
			sr.renderer.SetDrawColor(r, g, b, 220)
			sr.renderer.FillRect(&sdl.Rect{X: x, Y: y, W: w, H: barH})

			// Draw outline
			sr.renderer.SetDrawColor(r+20, g+20, b+20, 255)
			sr.renderer.DrawRect(&sdl.Rect{X: x, Y: y, W: w, H: barH})
		} else {
			// Draw a very thin baseline for zero bars
			sr.renderer.SetDrawColor(32, 100, 100, 100)
			sr.renderer.FillRect(&sdl.Rect{X: x, Y: int32(VisualizerHeight) - 1, W: w, H: 1})
		}
	}
}

// drawProgressBar draws the progress bar at the bottom
func (sr *SDLRenderer) drawProgressBar() {
	sr.displayMu.Lock()
	elapsed := sr.progress.Elapsed
	duration := sr.progress.Duration
	playState := sr.playState
	sr.displayMu.Unlock()

	// Draw background bar (dark teal)
	sr.renderer.SetDrawColor(20, 40, 40, 255)
	sr.renderer.FillRect(&sdl.Rect{
		X: 0,
		Y: int32(VisualizerHeight),
		W: WindowWidth,
		H: ProgressBarHeight,
	})

	// Draw border/separator line
	sr.renderer.SetDrawColor(32, 200, 191, 255)
	sr.renderer.DrawLine(0, int32(VisualizerHeight), WindowWidth, int32(VisualizerHeight))

	// Draw progress indicator
	if duration > 0 {
		progress := elapsed / duration
		if progress > 1.0 {
			progress = 1.0
		}

		progressWidth := int32(float64(WindowWidth) * progress)
		if progressWidth > 0 {
			sr.renderer.SetDrawColor(32, 200, 191, 200)
			sr.renderer.FillRect(&sdl.Rect{
				X: 0,
				Y: int32(VisualizerHeight),
				W: progressWidth,
				H: ProgressBarHeight,
			})
		}
	}

	// Draw time text and playback icon
	timeStr := formatTime(elapsed, duration)
	stateIcon := getPlayStateIcon(playState)

	// Render time text
	textSurface, err := sr.defaultFont.RenderUTF8Blended(timeStr, sdl.Color{R: 255, G: 255, B: 255, A: 255})
	if err == nil && textSurface != nil {
		defer textSurface.Free()
		texture, err := sr.renderer.CreateTextureFromSurface(textSurface)
		if err == nil && texture != nil {
			defer texture.Destroy()
			dstRect := &sdl.Rect{
				X: 10,
				Y: int32(VisualizerHeight + 15),
				W: textSurface.W,
				H: textSurface.H,
			}
			sr.renderer.Copy(texture, nil, dstRect)
		}
	}

	// Render playback state icon
	iconSurface, err := sr.iconFont.RenderUTF8Solid(stateIcon, sdl.Color{R: 255, G: 255, B: 255, A: 255})
	if err == nil && iconSurface != nil {
		defer iconSurface.Free()
		texture, err := sr.renderer.CreateTextureFromSurface(iconSurface)
		if err == nil && texture != nil {
			defer texture.Destroy()
			dstRect := &sdl.Rect{
				X: WindowWidth - 60,
				Y: int32(VisualizerHeight + 10),
				W: iconSurface.W,
				H: iconSurface.H,
			}
			sr.renderer.Copy(texture, nil, dstRect)
		}
	}

}

// drawSongInfoOverlay draws the song information overlay
func (sr *SDLRenderer) drawSongInfoOverlay() {
	sr.displayMu.Lock()
	song := sr.songInfo
	expiry := sr.songInfoExpiry
	sr.displayMu.Unlock()

	now := time.Now()
	if song.Title == "" || expiry.IsZero() || now.After(expiry) {
		return
	}

	if sr.largeFont == nil || sr.defaultFont == nil {
		return
	}

	// Semi-transparent background
	sr.renderer.SetDrawColor(0, 0, 0, 180)
	sr.renderer.FillRect(&sdl.Rect{
		X: 0,
		Y: 20,
		W: WindowWidth,
		H: 200,
	})

	// Draw track and title
	titleText := song.Title
	if song.Track != "" {
		titleText = fmt.Sprintf("%s. %s", song.Track, titleText)
	}

	y := int32(30)
	color := sdl.Color{R: 32, G: 200, B: 191, A: 255}

	// Artist
	if song.Artist != "" {
		surface, err := sr.defaultFont.RenderUTF8Blended(song.Artist, color)
		if err == nil && surface != nil {
			defer surface.Free()
			texture, err := sr.renderer.CreateTextureFromSurface(surface)
			if err == nil && texture != nil {
				defer texture.Destroy()
				sr.renderer.Copy(texture, nil, &sdl.Rect{X: 20, Y: y, W: surface.W, H: surface.H})
			}
		}
		y += 30
	}

	// Album
	if song.Album != "" {
		surface, err := sr.defaultFont.RenderUTF8Blended(song.Album, color)
		if err == nil && surface != nil {
			defer surface.Free()
			texture, err := sr.renderer.CreateTextureFromSurface(surface)
			if err == nil && texture != nil {
				defer texture.Destroy()
				sr.renderer.Copy(texture, nil, &sdl.Rect{X: 20, Y: y, W: surface.W, H: surface.H})
			}
		}
		y += 30
	}

	// Title
	if titleText != "" {
		surface, err := sr.largeFont.RenderUTF8Blended(titleText, color)
		if err == nil && surface != nil {
			defer surface.Free()
			texture, err := sr.renderer.CreateTextureFromSurface(surface)
			if err == nil && texture != nil {
				defer texture.Destroy()
				sr.renderer.Copy(texture, nil, &sdl.Rect{X: 20, Y: y, W: surface.W, H: surface.H})
			}
		}
	}
}

// Helper functions

func formatTime(elapsed, duration float64) string {
	elapsedMin := int(elapsed) / 60
	elapsedSec := int(elapsed) % 60
	durationMin := int(duration) / 60
	durationSec := int(duration) % 60

	return fmt.Sprintf("%d:%02d / %d:%02d", elapsedMin, elapsedSec, durationMin, durationSec)
}

func getPlayStateIcon(state string) string {
	switch state {
	case "play":
		return "▶"
	case "pause":
		return "⏸"
	case "stop":
		return "◼"
	default:
		return " "
	}
}
