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
	ProgressBarHeight = 40
	VisualizerHeight  = WindowHeight - ProgressBarHeight

	// Font paths - adjust for small displays like Raspberry Pi touchscreen
	DefaultFontPath = "/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf"
	IconFontPath    = "/usr/share/fonts/truetype/noto/NotoSansSymbols2-Regular.ttf" // Use a font with better symbols if needed

	DefaultFontSize         = 20
	LargeFontSize           = 40
	IconFontSize            = 24
	SongInfoDisplayDuration = 5 * time.Second
)

var (
	ColorBackground      = sdl.Color{R: 0, G: 0, B: 0, A: 255}
	ColorVisualizerEmpty = sdl.Color{R: 20, G: 80, B: 120, A: 255}
	ColorVisualizerLine  = sdl.Color{R: 255, G: 255, B: 255, A: 255}
	ColorVisualizerCross = sdl.Color{R: 255, G: 255, B: 0, A: 255}
	ColorVisualizerGrid  = sdl.Color{R: 30, G: 50, B: 50, A: 128}
	ColorZeroBar         = sdl.Color{R: 32, G: 100, B: 100, A: 100}
	ColorProgressBg      = sdl.Color{R: 20, G: 40, B: 40, A: 255}
	ColorProgressBorder  = sdl.Color{R: 32, G: 200, B: 191, A: 255}
	ColorProgressFill    = sdl.Color{R: 19, G: 102, B: 98, A: 200}
	ColorTimeText        = sdl.Color{R: 255, G: 255, B: 255, A: 255}
	ColorSongInfoBg      = sdl.Color{R: 0, G: 0, B: 0, A: 180}
	ColorSongInfoText    = sdl.Color{R: 32, G: 200, B: 191, A: 255}
	ColorTestText        = sdl.Color{R: 32, G: 196, B: 191, A: 255}
)

type SDLRenderer struct {
	window      *sdl.Window
	renderer    *sdl.Renderer
	target      *sdl.Texture
	defaultFont *ttf.Font
	largeFont   *ttf.Font
	iconFont    *ttf.Font
	rotate      bool

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
func InitSDL(rotateScreen bool) error {
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

	// Hide the cursor
	sdl.ShowCursor(sdl.DISABLE)

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
	renderer.SetDrawColor(ColorBackground.R, ColorBackground.G, ColorBackground.B, ColorBackground.A)
	renderer.Clear()
	renderer.Present()

	var target *sdl.Texture
	if rotateScreen {
		target, err = renderer.CreateTexture(sdl.PIXELFORMAT_ABGR8888, sdl.TEXTUREACCESS_TARGET, WindowWidth, WindowHeight)
		if err != nil {
			renderer.Destroy()
			window.Destroy()
			ttf.Quit()
			sdl.Quit()
			return fmt.Errorf("failed to create render target: %v", err)
		}
	}

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
		target:      target,
		defaultFont: defaultfont,
		largeFont:   largeFont,
		iconFont:    iconFont,
		rotate:      rotateScreen,
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
	if sdlRenderer.target != nil {
		sdlRenderer.target.Destroy()
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

	if sr.rotate && sr.target != nil {
		if err := sr.renderer.SetRenderTarget(sr.target); err != nil {
			logger.Warn("Failed to set SDL render target", "err", err)
		}
	}

	// Clear background
	sr.renderer.SetDrawColor(0, 0, 0, 255)
	sr.renderer.Clear()

	// Draw dynamic content
	sr.drawVisualizer()
	sr.drawProgressBar()
	sr.drawSongInfoOverlay()

	if sr.rotate && sr.target != nil {
		if err := sr.renderer.SetRenderTarget(nil); err != nil {
			logger.Warn("Failed to reset SDL render target", "err", err)
		}
		sr.renderer.SetDrawColor(ColorBackground.R, ColorBackground.G, ColorBackground.B, ColorBackground.A)
		sr.renderer.Clear()
		center := &sdl.Point{X: WindowWidth / 2, Y: WindowHeight / 2}
		dstRect := &sdl.Rect{X: 0, Y: 0, W: WindowWidth, H: WindowHeight}
		if err := sr.renderer.CopyEx(sr.target, nil, dstRect, 180, center, sdl.FLIP_NONE); err != nil {
			logger.Warn("Failed to copy rotated render target", "err", err)
		}
	}

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
		// Draw a dark background so the app is clearly visible when no data has arrived.
		sr.renderer.SetDrawColor(ColorVisualizerEmpty.R, ColorVisualizerEmpty.G, ColorVisualizerEmpty.B, ColorVisualizerEmpty.A)
		sr.renderer.FillRect(&sdl.Rect{X: 0, Y: 0, W: WindowWidth, H: VisualizerHeight})

		if sr.defaultFont != nil {
			text := "Music Player"
			color := ColorTestText
			surface, err := sr.defaultFont.RenderUTF8Blended(text, color)
			if err == nil && surface != nil {
				defer surface.Free()
				texture, err := sr.renderer.CreateTextureFromSurface(surface)
				if err == nil && texture != nil {
					defer texture.Destroy()
					dst := &sdl.Rect{
						X: WindowWidth/2 - surface.W/2,
						Y: VisualizerHeight/2 - surface.H/2,
						W: surface.W,
						H: surface.H,
					}
					sr.renderer.Copy(texture, nil, dst)
				}
			}
		}

		logger.Debug("Rendering Music Player test text - no bar data yet")
		return
	}

	barWidthFloat := float64(WindowWidth) / float64(numBars)
	barGap := 4 // larger gap between bars
	logger.Debug("Drawing visualizer", "bars", numBars, "bar_width_float", barWidthFloat, "bar_gap", barGap)

	// Always draw grid/baseline even if all bars are zero
	sr.renderer.SetDrawColor(ColorVisualizerGrid.R, ColorVisualizerGrid.G, ColorVisualizerGrid.B, ColorVisualizerGrid.A)
	for i := 0; i <= numBars; i++ {
		x := int32(float64(i) * barWidthFloat)
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
		x := int32(float64(i) * barWidthFloat)
		nextX := int32(float64(i+1) * barWidthFloat)
		w := nextX - x - int32(barGap)
		if w < 1 {
			w = 1
		}
		y := int32(VisualizerHeight - scaledHeight)
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
			sr.renderer.SetDrawColor(ColorZeroBar.R, ColorZeroBar.G, ColorZeroBar.B, ColorZeroBar.A)
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
	sr.renderer.SetDrawColor(ColorProgressBg.R, ColorProgressBg.G, ColorProgressBg.B, ColorProgressBg.A)
	sr.renderer.FillRect(&sdl.Rect{
		X: 0,
		Y: int32(VisualizerHeight),
		W: WindowWidth,
		H: ProgressBarHeight,
	})

	// Draw border/separator line
	sr.renderer.SetDrawColor(ColorProgressBorder.R, ColorProgressBorder.G, ColorProgressBorder.B, ColorProgressBorder.A)
	sr.renderer.DrawLine(0, int32(VisualizerHeight), WindowWidth, int32(VisualizerHeight))

	// Draw progress indicator
	if duration > 0 {
		progress := elapsed / duration
		if progress > 1.0 {
			progress = 1.0
		}

		progressWidth := int32(float64(WindowWidth) * progress)
		if progressWidth > 0 {
			sr.renderer.SetDrawColor(ColorProgressFill.R, ColorProgressFill.G, ColorProgressFill.B, ColorProgressFill.A)
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
	textSurface, err := sr.defaultFont.RenderUTF8Blended(timeStr, ColorTimeText)
	if err == nil && textSurface != nil {
		defer textSurface.Free()
		texture, err := sr.renderer.CreateTextureFromSurface(textSurface)
		if err == nil && texture != nil {
			defer texture.Destroy()
			dstRect := &sdl.Rect{
				X: 10,
				Y: int32(VisualizerHeight + 10),
				W: textSurface.W,
				H: textSurface.H,
			}
			sr.renderer.Copy(texture, nil, dstRect)
		}
	}

	// Render playback state icon
	iconSurface, err := sr.iconFont.RenderUTF8Solid(stateIcon, ColorTimeText)
	if err == nil && iconSurface != nil {
		defer iconSurface.Free()
		texture, err := sr.renderer.CreateTextureFromSurface(iconSurface)
		if err == nil && texture != nil {
			defer texture.Destroy()
			dstRect := &sdl.Rect{
				X: WindowWidth - 40,
				Y: int32(VisualizerHeight + 3),
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

	if sr.largeFont == nil {
		return
	}

	// Semi-transparent background
	sr.renderer.SetDrawColor(ColorSongInfoBg.R, ColorSongInfoBg.G, ColorSongInfoBg.B, ColorSongInfoBg.A)
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
	lineStep := int32(sr.largeFont.LineSkip())
	if lineStep <= 0 {
		lineStep = 30
	}
	color := ColorSongInfoText

	// Artist
	if song.Artist != "" {
		surface, err := sr.largeFont.RenderUTF8Blended(song.Artist, color)
		if err == nil && surface != nil {
			defer surface.Free()
			texture, err := sr.renderer.CreateTextureFromSurface(surface)
			if err == nil && texture != nil {
				defer texture.Destroy()
				sr.renderer.Copy(texture, nil, &sdl.Rect{X: 20, Y: y, W: surface.W, H: surface.H})
			}
		}
		y += lineStep
	}

	// Album
	if song.Album != "" {
		surface, err := sr.largeFont.RenderUTF8Blended(song.Album, color)
		if err == nil && surface != nil {
			defer surface.Free()
			texture, err := sr.renderer.CreateTextureFromSurface(surface)
			if err == nil && texture != nil {
				defer texture.Destroy()
				sr.renderer.Copy(texture, nil, &sdl.Rect{X: 20, Y: y, W: surface.W, H: surface.H})
			}
		}
		y += lineStep
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
