package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
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
	"path/filepath"
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
	eyelashCount         = 5
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
	Emotion string `json:"emotion"`
	Data    struct {
		Tamagotchi struct {
			Emotion string `json:"emotion"`
		} `json:"tamagotchi"`
	} `json:"data"`
}

type renderer interface {
	Display(emotion string) error
}

type matrixRenderer struct {
	binary         string
	args           []string
	anim           time.Duration
	width          int
	height         int
	currentEmotion string
	currentCmd     *exec.Cmd
	currentCancel  context.CancelFunc
	currentPath    string
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
	previewEmotion := flag.String("preview", "", "write a GIF preview for an emotion")
	previewOutput := flag.String("output", "", "GIF preview output path")
	flag.Parse()
	if *previewEmotion != "" {
		if err := writePreview(*previewEmotion, *previewOutput); err != nil {
			log.Fatalf("preview failed: %v", err)
		}
		return
	}

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
			if displayErr := r.DisplayMessage("NETWORK ERROR"); displayErr != nil {
				logger.Printf("display failed for network error: %v", displayErr)
			}
			return
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

func writePreview(emotion, outputPath string) error {
	emotion = normalizeEmotion(emotion, "happy")
	if outputPath == "" {
		outputPath = filepath.Join("previews", emotion+".gif")
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return err
	}
	if err := writeGIF(outputPath, animationForEmotion(emotion, 64, 64)); err != nil {
		return err
	}
	log.Printf("wrote %s preview to %s", emotion, outputPath)
	return nil
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
		"--led-pixel-mapper=Rotate:90",
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
	emotion := strings.TrimSpace(payload.Emotion)
	if emotion == "" {
		emotion = strings.TrimSpace(payload.Data.Tamagotchi.Emotion)
	}
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
	case "excited", "excitement", "thrilled", "energetic":
		return "excited"
	case "sad", "sadness", "unhappy", "sorrow", "frown", "frowning":
		return "sad"
	case "calm", "relaxed", "peaceful", "serene":
		return "calm"
	case "sleep", "sleepy", "asleep", "sleeping", "tired", "drowsy":
		return "sleep"
	default:
		return strings.ToLower(strings.TrimSpace(fallback))
	}
}

func (r *matrixRenderer) Display(emotion string) error {
	if strings.TrimSpace(emotion) == "" {
		emotion = "happy"
	}
	if emotion == r.currentEmotion && r.currentCmd != nil && r.currentCmd.Process != nil {
		return nil
	}
	return r.displayAnimation(emotion, animationForEmotion(emotion, r.width, r.height))
}

func (r *matrixRenderer) DisplayMessage(message string) error {
	if message == "" {
		return nil
	}
	return r.displayAnimation("message:"+message, renderTextAnimation(message, r.width, r.height))
}

func (r *matrixRenderer) displayAnimation(emotion string, anim *gif.GIF) error {
	if emotion == r.currentEmotion && r.currentCmd != nil && r.currentCmd.Process != nil {
		return nil
	}
	if err := r.stop(); err != nil {
		return err
	}

	tmpFile, err := os.CreateTemp("", "baendaeli-client-led-eye-*.gif")
	if err != nil {
		return err
	}
	outputPath := tmpFile.Name()
	if err := tmpFile.Close(); err != nil {
		return err
	}

	if err := writeGIF(outputPath, anim); err != nil {
		_ = os.Remove(outputPath)
		return err
	}

	if _, err := exec.LookPath(r.binary); err != nil {
		_ = os.Remove(outputPath)
		return fmt.Errorf("matrix binary not found (%s): %w", r.binary, err)
	}

	args := append([]string{}, r.args...)
	args = append(args, outputPath)

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, r.binary, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		cancel()
		_ = os.Remove(outputPath)
		return fmt.Errorf("%w (%s)", err, strings.TrimSpace(stderr.String()))
	}
	r.currentEmotion = emotion
	r.currentCmd = cmd
	r.currentCancel = cancel
	r.currentPath = outputPath
	return nil
}

func (r *matrixRenderer) stop() error {
	if r.currentCmd == nil {
		return nil
	}
	r.currentCancel()
	_ = r.currentCmd.Wait()
	if r.currentPath != "" {
		_ = os.Remove(r.currentPath)
	}
	r.currentEmotion = ""
	r.currentCmd = nil
	r.currentCancel = nil
	r.currentPath = ""
	return nil
}

func animationForEmotion(emotion string, width, height int) *gif.GIF {
	switch emotion {
	case "excited":
		return renderExcitedEyeAnimation(width, height)
	case "sad":
		return renderSadEyeAnimation(width, height)
	case "calm":
		return renderCalmEyeAnimation(width, height)
	case "sleep":
		return renderSleepEyeAnimation(width, height)
	default:
		return renderHappyEyeAnimation(width, height)
	}
}

var bitmapFont = map[rune][]string{
	'A': {"01110", "10001", "10001", "11111", "10001", "10001", "10001"},
	'E': {"11111", "10000", "10000", "11110", "10000", "10000", "11111"},
	'K': {"10001", "10010", "10100", "11000", "10100", "10010", "10001"},
	'N': {"10001", "11001", "10101", "10011", "10001", "10001", "10001"},
	'O': {"01110", "10001", "10001", "10001", "10001", "10001", "01110"},
	'R': {"11110", "10001", "10001", "11110", "10100", "10010", "10001"},
	'T': {"11111", "00100", "00100", "00100", "00100", "00100", "00100"},
	'W': {"10001", "10001", "10001", "10101", "10101", "11011", "10001"},
}

func renderTextAnimation(message string, width, height int) *gif.GIF {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	fillRect(img, color.RGBA{0, 0, 0, 255})
	lines := strings.Fields(strings.ToUpper(message))
	lineHeight := 9
	startY := (height - len(lines)*lineHeight) / 2
	for lineIndex, line := range lines {
		lineWidth := len(line)*6 - 1
		startX := (width - lineWidth) / 2
		for charIndex, char := range line {
			pattern, ok := bitmapFont[char]
			if !ok {
				continue
			}
			for y, row := range pattern {
				for x, pixel := range row {
					if pixel == '1' {
						img.SetRGBA(startX+charIndex*6+x, startY+lineIndex*lineHeight+y, color.RGBA{185, 35, 100, 255})
					}
				}
			}
		}
	}

	palette := color.Palette{
		color.RGBA{0, 0, 0, 255},
		color.RGBA{185, 35, 100, 255},
	}
	paletted := image.NewPaletted(img.Bounds(), palette)
	draw.FloydSteinberg.Draw(paletted, paletted.Rect, img, image.Point{})
	return &gif.GIF{Image: []*image.Paletted{paletted}, Delay: []int{100}, LoopCount: 0}
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
		{blink: 0.00, look: 0.00, delay: 400},
		{blink: 0.12, look: 0.08, delay: 12},
		{blink: 0.45, look: 0.12, delay: 10},
		{blink: 0.80, look: 0.00, delay: 8},
		{blink: 0.45, look: -0.12, delay: 10},
		{blink: 0.12, look: -0.08, delay: 12},
		{blink: 0.00, look: 0.00, delay: 450},
	}

	palette := color.Palette{
		color.RGBA{0, 0, 0, 255},
		color.RGBA{255, 255, 255, 255},
		color.RGBA{185, 35, 100, 255},
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
		{blink: 0.10, look: 0.00, delay: 400},
		{blink: 0.24, look: -0.04, delay: 12},
		{blink: 0.55, look: 0.00, delay: 10},
		{blink: 0.24, look: 0.04, delay: 12},
		{blink: 0.10, look: 0.00, delay: 450},
	}

	palette := color.Palette{
		color.RGBA{0, 0, 0, 255},
		color.RGBA{255, 255, 255, 255},
		color.RGBA{185, 35, 100, 255},
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

func renderExcitedEyeAnimation(width, height int) *gif.GIF {
	timeline := []struct {
		blink float64
		look  float64
		delay int
	}{
		{blink: 0.00, look: -0.10, delay: 250},
		{blink: 0.08, look: 0.10, delay: 12},
		{blink: 0.35, look: 0.00, delay: 8},
		{blink: 0.08, look: 0.00, delay: 12},
		{blink: 0.00, look: 0.00, delay: 280},
	}

	palette := color.Palette{
		color.RGBA{0, 0, 0, 255},
		color.RGBA{255, 255, 255, 255},
		color.RGBA{185, 35, 100, 255},
	}
	anim := &gif.GIF{LoopCount: 0}
	for _, step := range timeline {
		frame := renderExpressionEyeFrame(width, height, step.blink, step.look, 0.04, -0.0025, 0)
		eyeRadius := math.Min(float64(width), float64(height)) * 0.42
		drawEyebrow(frame, float64(width)/2, float64(height)/2-eyeRadius*0.92, eyeRadius*0.85, 0.014, 0.020, color.RGBA{185, 35, 100, 255})
		paletted := image.NewPaletted(frame.Bounds(), palette)
		draw.FloydSteinberg.Draw(paletted, paletted.Rect, frame, image.Point{})
		anim.Image = append(anim.Image, paletted)
		anim.Delay = append(anim.Delay, step.delay)
	}
	return anim
}

func renderCalmEyeAnimation(width, height int) *gif.GIF {
	timeline := []struct {
		blink float64
		look  float64
		delay int
	}{
		{blink: 0.35, look: -0.03, delay: 400},
		{blink: 0.48, look: 0.00, delay: 16},
		{blink: 0.35, look: 0.03, delay: 400},
	}

	palette := color.Palette{
		color.RGBA{0, 0, 0, 255},
		color.RGBA{255, 255, 255, 255},
		color.RGBA{185, 35, 100, 255},
	}
	anim := &gif.GIF{LoopCount: 0}
	for _, step := range timeline {
		frame := renderExpressionEyeFrame(width, height, step.blink, step.look, 0.02, 0.0012, 0)
		eyeRadius := math.Min(float64(width), float64(height)) * 0.42
		drawEyebrow(frame, float64(width)/2, float64(height)/2-eyeRadius*0.75, eyeRadius*0.75, 0.001, 0, color.RGBA{185, 35, 100, 255})
		paletted := image.NewPaletted(frame.Bounds(), palette)
		draw.FloydSteinberg.Draw(paletted, paletted.Rect, frame, image.Point{})
		anim.Image = append(anim.Image, paletted)
		anim.Delay = append(anim.Delay, step.delay)
	}
	return anim
}

func renderSleepEyeAnimation(width, height int) *gif.GIF {
	timeline := []struct {
		offset float64
		delay  int
	}{
		{offset: 0.00, delay: 500},
		{offset: 0.015, delay: 500},
	}

	palette := color.Palette{
		color.RGBA{0, 0, 0, 255},
		color.RGBA{185, 35, 100, 255},
	}

	anim := &gif.GIF{LoopCount: 0}
	for _, step := range timeline {
		frame := renderSleepEyeFrame(width, height, step.offset)
		paletted := image.NewPaletted(frame.Bounds(), palette)
		draw.FloydSteinberg.Draw(paletted, paletted.Rect, frame, image.Point{})
		anim.Image = append(anim.Image, paletted)
		anim.Delay = append(anim.Delay, step.delay)
	}

	return anim
}

// renderSleepEyeFrame draws a fully closed lid as a downward-arcing curve with hanging lashes.
func renderSleepEyeFrame(width, height int, offset float64) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	accent := color.RGBA{185, 35, 100, 255}
	fillRect(img, color.RGBA{0, 0, 0, 255})

	cx, cy := float64(width)/2, float64(height)/2
	outerRadius := math.Min(float64(width), float64(height)) * 0.42
	lidY := cy + outerRadius*offset
	const lidCurve = -0.016

	drawClosedLid(img, cx, lidY, outerRadius*0.85, lidCurve, 0, accent)
	drawClosedLashes(img, cx, lidY, outerRadius, lidCurve, accent)

	return img
}

func drawClosedLid(img *image.RGBA, cx, cy, halfWidth, curve, tilt float64, c color.RGBA) {
	for x := int(cx - halfWidth); x <= int(cx+halfWidth); x++ {
		xf := float64(x) - cx
		y := cy + curve*xf*xf + tilt*xf
		for thickness := -1.5; thickness <= 1.5; thickness += 0.3 {
			setSafe(img, x, int(y+thickness), c)
		}
	}
}

func drawClosedLashes(img *image.RGBA, cx, cy, radius, curve float64, c color.RGBA) {
	for lash := 0; lash < eyelashCount; lash++ {
		position := -0.56 + float64(lash)*0.28
		x := cx + radius*position
		xf := x - cx
		y := cy + curve*xf*xf
		for step := 0; step < 4; step++ {
			setSafe(img, int(x), int(y+float64(step)), c)
		}
	}
}

func renderHappyEyeFrame(width, height int, blink float64, look float64) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	bg := color.RGBA{0, 0, 0, 255}
	white := color.RGBA{255, 255, 255, 255}
	black := color.RGBA{0, 0, 0, 255}
	accent := color.RGBA{185, 35, 100, 255}

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
	fillUpperEyelid(img, cx, cy, outerRadius, blink)
	fillLowerEyelid(img, cx, cy, outerRadius, blink, 0.0035, 0)
	drawUpperLashes(img, cx, cy, outerRadius, blink, 0.62, 0.68, 0.0035, 0, 4, accent)
	drawEyebrow(img, cx, cy-outerRadius*0.88, outerRadius*0.80, 0.005, 0, accent)

	return img
}

func fillUpperEyelid(img *image.RGBA, cx, cy, radius, blink float64) {
	for x := 0; x < img.Bounds().Dx(); x++ {
		xf := float64(x) - cx
		eyelidY := cy - radius*(0.62-0.68*blink) + 0.0035*xf*xf
		for y := 0; y <= int(eyelidY); y++ {
			setSafe(img, x, y, color.RGBA{0, 0, 0, 255})
		}
	}
}

func fillLowerEyelid(img *image.RGBA, cx, cy, radius, blink, curve, tilt float64) {
	for x := 0; x < img.Bounds().Dx(); x++ {
		xf := float64(x) - cx
		eyelidY := cy + radius*(0.62-0.68*blink) - curve*xf*xf + tilt*xf
		for y := int(eyelidY); y < img.Bounds().Dy(); y++ {
			setSafe(img, x, y, color.RGBA{0, 0, 0, 255})
		}
	}
}

func renderSadEyeFrame(width, height int, blink float64, look float64) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	bg := color.RGBA{0, 0, 0, 255}
	white := color.RGBA{255, 255, 255, 255}
	black := color.RGBA{0, 0, 0, 255}
	accent := color.RGBA{185, 35, 100, 255}

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
	fillCircle(img, cx+pupilShift, cy+outerRadius*0.12, pupilRadius, black)
	fillCircle(img, cx+pupilShift-pupilRadius*0.35, cy+outerRadius*0.12-pupilRadius*0.35, highlightRadius, white)
	fillExpressionEyelid(img, cx, cy, outerRadius, blink, -0.005, 0)
	fillLowerEyelid(img, cx, cy, outerRadius, blink, -0.005, 0)
	drawUpperLashes(img, cx, cy, outerRadius, blink, 0.66, 0.72, -0.005, 0, 24, accent)
	drawEyebrow(img, cx, cy-outerRadius*0.90, outerRadius*0.82, -0.008, -0.10, accent)

	return img
}

func renderExpressionEyeFrame(width, height int, blink, look, pupilOffset, curve, tilt float64) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	bg := color.RGBA{0, 0, 0, 255}
	white := color.RGBA{255, 255, 255, 255}
	black := color.RGBA{0, 0, 0, 255}
	accent := color.RGBA{185, 35, 100, 255}

	fillRect(img, bg)
	cx, cy := float64(width)/2, float64(height)/2
	outerRadius := math.Min(float64(width), float64(height)) * 0.42
	innerRadius := outerRadius * 0.95
	pupilRadius := outerRadius * (0.24 - 0.12*blink)
	if pupilRadius < outerRadius*0.08 {
		pupilRadius = outerRadius * 0.08
	}
	pupilShift := look * outerRadius * 0.25
	pupilY := cy + outerRadius*pupilOffset

	fillCircle(img, cx, cy, outerRadius, accent)
	fillCircle(img, cx, cy, innerRadius, white)
	fillCircle(img, cx+pupilShift, pupilY, pupilRadius, black)
	fillCircle(img, cx+pupilShift-pupilRadius*0.35, pupilY-pupilRadius*0.35, pupilRadius*0.28, white)

	fillExpressionEyelid(img, cx, cy, outerRadius, blink, curve, tilt)
	fillLowerEyelid(img, cx, cy, outerRadius, blink, curve, tilt)
	drawUpperLashes(img, cx, cy, outerRadius, blink, 0.66, 0.72, curve, tilt, 4, accent)
	return img
}

func fillExpressionEyelid(img *image.RGBA, cx, cy, radius, blink, curve, tilt float64) {
	for x := 0; x < img.Bounds().Dx(); x++ {
		xf := float64(x) - cx
		eyelidY := cy - radius*(0.66-0.72*blink) + curve*xf*xf + tilt*xf
		for y := 0; y <= int(eyelidY); y++ {
			setSafe(img, x, y, color.RGBA{0, 0, 0, 255})
		}
	}
}

func drawUpperLashes(img *image.RGBA, cx, cy, radius, blink, lidOffset, blinkScale, curve, tilt float64, length int, c color.RGBA) {
	for lash := 0; lash < eyelashCount; lash++ {
		position := -0.56 + float64(lash)*0.28
		x := cx + radius*position
		xf := x - cx
		y := cy - radius*(lidOffset-blinkScale*blink) + curve*xf*xf + tilt*xf
		direction := 0.0
		if position < 0 {
			direction = -1
		} else if position > 0 {
			direction = 1
		}
		for step := 0; step < length; step++ {
			setSafe(img, int(x+direction*float64(step)/2), int(y-float64(step)), c)
		}
	}
}
func drawEyebrow(img *image.RGBA, cx, cy, halfWidth, curve, tilt float64, c color.RGBA) {
	for x := int(cx - halfWidth); x <= int(cx+halfWidth); x++ {
		xf := float64(x) - cx
		y := cy + curve*xf*xf + tilt*xf
		for thickness := -1.4; thickness <= 1.4; thickness += 0.3 {
			setSafe(img, x, int(y+thickness), c)
		}
	}
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
