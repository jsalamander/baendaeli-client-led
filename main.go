package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"log"
	"math"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/jsalamander/baendaeli-client-led/internal/version"
)

const (
	defaultAPIURL        = "https://www.baendae.li/api/public/tamagotchi"
	defaultPollInterval  = 2 * time.Second
	defaultMatrixBinary  = "led-image-viewer"
	defaultMatrixMapping = "adafruit-hat"
	defaultSoundBinary   = "speaker-test"
)

type config struct {
	APIURL          string
	PollInterval    time.Duration
	MatrixBinary    string
	MatrixArgs      []string
	AnimationTime   time.Duration
	Width           int
	Height          int
	FallbackEmotion string
	SoundEnabled    bool
	SoundBinary     string
	SoundArgs       []string
}

type tamagotchiAPIResponse struct {
	Data struct {
		Tamagotchi struct {
			Emotion string `json:"emotion"`
		} `json:"tamagotchi"`
	} `json:"data"`
}

type renderer interface {
	Display(emotion string) error
}

type matrixRenderer struct {
	binary string
	args   []string
	anim   time.Duration
	width  int
	height int
}

type beepPlayer struct {
	binary string
	args   []string
}

func (p *beepPlayer) Play() error {
	if _, err := exec.LookPath(p.binary); err != nil {
		return fmt.Errorf("sound binary not found (%s): %w", p.binary, err)
	}

	cmd := exec.Command(p.binary, p.args...)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() {
		_ = cmd.Wait()
	}()
	return nil
}

func main() {
	cfg := loadConfig()
	logger := log.New(os.Stdout, "", log.LstdFlags)
	logger.Printf("starting baendaeli-client-led version=%s", version.AppVersion)

	r := &matrixRenderer{
		binary: cfg.MatrixBinary,
		args:   cfg.MatrixArgs,
		anim:   cfg.AnimationTime,
		width:  cfg.Width,
		height: cfg.Height,
	}
	beeper := &beepPlayer{binary: cfg.SoundBinary, args: cfg.SoundArgs}

	client := &http.Client{Timeout: 10 * time.Second}
	pollAndDisplay := func() {
		emotion, err := fetchEmotion(context.Background(), client, cfg.APIURL)
		if err != nil {
			logger.Printf("poll failed: %v", err)
			emotion = cfg.FallbackEmotion
		}
		normalized := normalizeEmotion(emotion, cfg.FallbackEmotion)
		if cfg.SoundEnabled {
			if err := beeper.Play(); err != nil {
				logger.Printf("beep failed: %v", err)
			}
		}
		if err := r.Display(normalized); err != nil {
			logger.Printf("display failed for emotion=%s: %v", normalized, err)
			return
		}
		logger.Printf("displayed emotion=%s", normalized)
	}

	pollAndDisplay()
	ticker := time.NewTicker(cfg.PollInterval)
	defer ticker.Stop()
	for range ticker.C {
		pollAndDisplay()
	}
}

func loadConfig() config {
	pollInterval := defaultPollInterval
	if raw := strings.TrimSpace(os.Getenv("POLL_INTERVAL_SECONDS")); raw != "" {
		if seconds, err := strconv.Atoi(raw); err == nil && seconds > 0 {
			pollInterval = time.Duration(seconds) * time.Second
		}
	}

	matrixBinary := strings.TrimSpace(os.Getenv("MATRIX_BINARY"))
	if matrixBinary == "" {
		matrixBinary = defaultMatrixBinary
	}

	matrixArgs := []string{
		"--led-rows=64",
		"--led-cols=64",
		"--led-chain=1",
		"--led-parallel=1",
		fmt.Sprintf("--led-gpio-mapping=%s", defaultMatrixMapping),
		"--led-brightness=60",
		"--led-no-drop-privs",
	}
	if raw := strings.TrimSpace(os.Getenv("MATRIX_ARGS")); raw != "" {
		matrixArgs = splitArgs(raw)
	}

	apiURL := strings.TrimSpace(os.Getenv("BAENDAELI_URL"))
	if apiURL == "" {
		apiURL = defaultAPIURL
	}

	fallbackEmotion := strings.TrimSpace(os.Getenv("FALLBACK_EMOTION"))
	if fallbackEmotion == "" {
		fallbackEmotion = "happy"
	}

	animationTime := pollInterval
	if raw := strings.TrimSpace(os.Getenv("ANIMATION_SECONDS")); raw != "" {
		if seconds, err := strconv.Atoi(raw); err == nil && seconds > 0 {
			animationTime = time.Duration(seconds) * time.Second
		}
	}

	soundEnabled := true
	if raw := strings.TrimSpace(os.Getenv("SOUND_ENABLED")); raw != "" {
		if enabled, err := strconv.ParseBool(raw); err == nil {
			soundEnabled = enabled
		}
	}

	soundBinary := strings.TrimSpace(os.Getenv("SOUND_BINARY"))
	if soundBinary == "" {
		soundBinary = defaultSoundBinary
	}

	soundArgs := []string{"-q", "-t", "sine", "-f", "880", "-l", "1"}
	if raw := strings.TrimSpace(os.Getenv("SOUND_ARGS")); raw != "" {
		soundArgs = splitArgs(raw)
	}

	return config{
		APIURL:          apiURL,
		PollInterval:    pollInterval,
		MatrixBinary:    matrixBinary,
		MatrixArgs:      matrixArgs,
		AnimationTime:   animationTime,
		Width:           64,
		Height:          64,
		FallbackEmotion: fallbackEmotion,
		SoundEnabled:    soundEnabled,
		SoundBinary:     soundBinary,
		SoundArgs:       soundArgs,
	}
}

func splitArgs(raw string) []string {
	parts := strings.Fields(raw)
	if len(parts) == 0 {
		return nil
	}
	return parts
}

func fetchEmotion(ctx context.Context, client *http.Client, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("unexpected status: %s", resp.Status)
	}

	var payload tamagotchiAPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", err
	}
	emotion := strings.TrimSpace(payload.Data.Tamagotchi.Emotion)
	if emotion == "" {
		return "", fmt.Errorf("emotion not found in response")
	}
	return emotion, nil
}

func normalizeEmotion(emotion string, fallback string) string {
	normalized := strings.ToLower(strings.TrimSpace(emotion))
	switch normalized {
	case "happy", "happiness", "joy", "smile", "smiling":
		return "happy"
	case "sad", "sadness", "unhappy", "sorrow", "frown", "frowning":
		return "sad"
	default:
		return strings.ToLower(strings.TrimSpace(fallback))
	}
}

func (r *matrixRenderer) Display(emotion string) error {
	if strings.TrimSpace(emotion) == "" {
		emotion = "happy"
	}

	var anim *gif.GIF
	switch emotion {
	case "sad":
		anim = renderSadEyeAnimation(r.width, r.height)
	default:
		anim = renderHappyEyeAnimation(r.width, r.height)
	}

	tmpFile, err := os.CreateTemp("", "baendaeli-client-led-eye-*.gif")
	if err != nil {
		return err
	}
	outputPath := tmpFile.Name()
	if err := tmpFile.Close(); err != nil {
		return err
	}
	defer os.Remove(outputPath)

	if err := writeGIF(outputPath, anim); err != nil {
		return err
	}

	if _, err := exec.LookPath(r.binary); err != nil {
		return fmt.Errorf("matrix binary not found (%s): %w", r.binary, err)
	}

	args := append([]string{}, r.args...)
	if !hasTimingArgs(args) {
		args = append(args, "-t", formatSeconds(r.anim))
	}
	args = append(args, outputPath)

	maxRun := r.anim + 2*time.Second
	if maxRun < 5*time.Second {
		maxRun = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), maxRun)
	defer cancel()

	cmd := exec.CommandContext(ctx, r.binary, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil
		}
		return fmt.Errorf("%w (%s)", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func hasTimingArgs(args []string) bool {
	for i := range args {
		arg := args[i]
		if arg == "-t" || arg == "-w" || arg == "-l" {
			return true
		}
		if strings.HasPrefix(arg, "-t") || strings.HasPrefix(arg, "-w") || strings.HasPrefix(arg, "-l") {
			return true
		}
	}
	return false
}

func formatSeconds(d time.Duration) string {
	seconds := d.Seconds()
	if seconds <= 0 {
		seconds = 1
	}
	return strconv.FormatFloat(seconds, 'f', 2, 64)
}

func writeGIF(path string, anim *gif.GIF) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return gif.EncodeAll(f, anim)
}

func renderHappyEyeAnimation(width, height int) *gif.GIF {
	timeline := []struct {
		blink float64
		look  float64
		delay int
	}{
		{blink: 0.00, look: 0.00, delay: 9},
		{blink: 0.15, look: 0.08, delay: 4},
		{blink: 0.55, look: 0.12, delay: 2},
		{blink: 1.00, look: 0.00, delay: 1},
		{blink: 0.55, look: -0.12, delay: 2},
		{blink: 0.15, look: -0.08, delay: 4},
		{blink: 0.00, look: 0.00, delay: 11},
	}

	palette := color.Palette{
		color.RGBA{0, 0, 0, 255},
		color.RGBA{255, 255, 255, 255},
		color.RGBA{255, 220, 70, 255},
	}

	anim := &gif.GIF{LoopCount: 0}
	for _, step := range timeline {
		frame := renderHappyEyeFrame(width, height, step.blink, step.look)
		paletted := image.NewPaletted(frame.Bounds(), palette)
		draw.FloydSteinberg.Draw(paletted, paletted.Rect, frame, image.Point{})
		anim.Image = append(anim.Image, paletted)
		anim.Delay = append(anim.Delay, step.delay)
	}

	return anim
}

func renderSadEyeAnimation(width, height int) *gif.GIF {
	timeline := []struct {
		blink float64
		look  float64
		delay int
	}{
		{blink: 0.00, look: 0.00, delay: 12},
		{blink: 0.20, look: -0.06, delay: 3},
		{blink: 0.75, look: 0.00, delay: 2},
		{blink: 1.00, look: 0.00, delay: 1},
		{blink: 0.75, look: 0.06, delay: 2},
		{blink: 0.20, look: 0.00, delay: 3},
		{blink: 0.00, look: 0.00, delay: 14},
	}

	palette := color.Palette{
		color.RGBA{0, 0, 0, 255},
		color.RGBA{255, 255, 255, 255},
		color.RGBA{80, 160, 255, 255},
	}

	anim := &gif.GIF{LoopCount: 0}
	for _, step := range timeline {
		frame := renderSadEyeFrame(width, height, step.blink, step.look)
		paletted := image.NewPaletted(frame.Bounds(), palette)
		draw.FloydSteinberg.Draw(paletted, paletted.Rect, frame, image.Point{})
		anim.Image = append(anim.Image, paletted)
		anim.Delay = append(anim.Delay, step.delay)
	}

	return anim
}

func renderHappyEyeFrame(width, height int, blink float64, look float64) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	bg := color.RGBA{0, 0, 0, 255}
	white := color.RGBA{255, 255, 255, 255}
	black := color.RGBA{0, 0, 0, 255}
	accent := color.RGBA{255, 220, 70, 255}

	fillRect(img, bg)

	cx, cy := float64(width)/2, float64(height)/2
	outerRadius := math.Min(float64(width), float64(height)) * 0.42
	innerRadius := outerRadius * 0.95
	pupilRadius := outerRadius * (0.24 - 0.12*blink)
	if pupilRadius < outerRadius*0.08 {
		pupilRadius = outerRadius * 0.08
	}
	highlightRadius := pupilRadius * 0.28
	pupilShift := look * outerRadius * 0.25

	fillCircle(img, cx, cy, outerRadius, accent)
	fillCircle(img, cx, cy, innerRadius, white)
	fillCircle(img, cx+pupilShift, cy, pupilRadius, black)
	fillCircle(img, cx+pupilShift-pupilRadius*0.35, cy-pupilRadius*0.35, highlightRadius, white)

	// smiling eyelid arc
	arcY := cy + outerRadius*(0.15+0.45*blink)
	thickness := 1.2 + 1.4*blink
	for x := 0; x < width; x++ {
		xf := float64(x) - cx
		y := arcY + (0.0035+0.0008*blink)*xf*xf
		for t := -thickness; t <= thickness; t += 0.3 {
			setSafe(img, x, int(y+t), black)
		}
	}

	return img
}

func renderSadEyeFrame(width, height int, blink float64, look float64) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	bg := color.RGBA{0, 0, 0, 255}
	white := color.RGBA{255, 255, 255, 255}
	black := color.RGBA{0, 0, 0, 255}
	accent := color.RGBA{80, 160, 255, 255}

	fillRect(img, bg)

	cx, cy := float64(width)/2, float64(height)/2
	outerRadius := math.Min(float64(width), float64(height)) * 0.42
	innerRadius := outerRadius * 0.95
	pupilRadius := outerRadius * (0.24 - 0.12*blink)
	if pupilRadius < outerRadius*0.08 {
		pupilRadius = outerRadius * 0.08
	}
	highlightRadius := pupilRadius * 0.28
	pupilShift := look * outerRadius * 0.25

	fillCircle(img, cx, cy, outerRadius, accent)
	fillCircle(img, cx, cy, innerRadius, white)
	fillCircle(img, cx+pupilShift, cy+outerRadius*0.10, pupilRadius, black)
	fillCircle(img, cx+pupilShift-pupilRadius*0.35, cy+outerRadius*0.10-pupilRadius*0.35, highlightRadius, white)

	arcY := cy - outerRadius*(0.15+0.45*blink)
	thickness := 1.2 + 1.4*blink
	for x := 0; x < width; x++ {
		xf := float64(x) - cx
		y := arcY - (0.0035+0.0008*blink)*xf*xf
		for t := -thickness; t <= thickness; t += 0.3 {
			setSafe(img, x, int(y+t), black)
		}
	}

	return img
}

func fillRect(img *image.RGBA, c color.RGBA) {
	for y := 0; y < img.Bounds().Dy(); y++ {
		for x := 0; x < img.Bounds().Dx(); x++ {
			img.SetRGBA(x, y, c)
		}
	}
}

func fillCircle(img *image.RGBA, cx, cy, radius float64, c color.RGBA) {
	r2 := radius * radius
	for y := 0; y < img.Bounds().Dy(); y++ {
		for x := 0; x < img.Bounds().Dx(); x++ {
			dx := float64(x) - cx
			dy := float64(y) - cy
			if dx*dx+dy*dy <= r2 {
				img.SetRGBA(x, y, c)
			}
		}
	}
}

func setSafe(img *image.RGBA, x, y int, c color.RGBA) {
	if image.Pt(x, y).In(img.Bounds()) {
		img.SetRGBA(x, y, c)
	}
}
