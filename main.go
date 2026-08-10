package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
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
	defaultPollInterval  = 30 * time.Second
	defaultMatrixBinary  = "led-image-viewer"
	defaultMatrixMapping = "adafruit-hat-pwm"
)

type config struct {
	APIURL         string
	PollInterval   time.Duration
	MatrixBinary   string
	MatrixArgs     []string
	Width          int
	Height         int
	FallbackEmotion string
}

type renderer interface {
	Display(emotion string) error
}

type matrixRenderer struct {
	binary string
	args   []string
	width  int
	height int
}

func main() {
	cfg := loadConfig()
	logger := log.New(os.Stdout, "", log.LstdFlags)
	logger.Printf("starting baendaeli-client-led version=%s", version.AppVersion)

	r := &matrixRenderer{
		binary: cfg.MatrixBinary,
		args:   cfg.MatrixArgs,
		width:  cfg.Width,
		height: cfg.Height,
	}

	client := &http.Client{Timeout: 10 * time.Second}
	pollAndDisplay := func() {
		emotion, err := fetchEmotion(context.Background(), client, cfg.APIURL)
		if err != nil {
			logger.Printf("poll failed: %v", err)
			emotion = cfg.FallbackEmotion
		}
		normalized := normalizeEmotion(emotion, cfg.FallbackEmotion)
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

	return config{
		APIURL:          apiURL,
		PollInterval:    pollInterval,
		MatrixBinary:    matrixBinary,
		MatrixArgs:      matrixArgs,
		Width:           64,
		Height:          64,
		FallbackEmotion: fallbackEmotion,
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

	var payload any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", err
	}
	if emotion, ok := findEmotion(payload); ok {
		return emotion, nil
	}
	return "", fmt.Errorf("emotion not found in response")
}

func findEmotion(payload any) (string, bool) {
	switch v := payload.(type) {
	case map[string]any:
		for _, key := range []string{"emotion", "mood", "state", "status"} {
			if raw, ok := v[key]; ok {
				if emotion := extractString(raw); emotion != "" {
					return emotion, true
				}
			}
		}
		for _, child := range v {
			if emotion, ok := findEmotion(child); ok {
				return emotion, true
			}
		}
	case []any:
		for _, child := range v {
			if emotion, ok := findEmotion(child); ok {
				return emotion, true
			}
		}
	case string:
		if strings.TrimSpace(v) != "" {
			return v, true
		}
	}
	return "", false
}

func extractString(v any) string {
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(s)
}

func normalizeEmotion(emotion string, fallback string) string {
	normalized := strings.ToLower(strings.TrimSpace(emotion))
	switch normalized {
	case "happy", "happiness", "joy", "smile", "smiling":
		return "happy"
	default:
		return strings.ToLower(strings.TrimSpace(fallback))
	}
}

func (r *matrixRenderer) Display(emotion string) error {
	if strings.TrimSpace(emotion) == "" {
		emotion = "happy"
	}
	if emotion != "happy" {
		emotion = "happy"
	}

	img := renderHappyEye(r.width, r.height)
	outputPath := filepath.Join(os.TempDir(), "baendaeli-client-led-eye.png")
	if err := writePNG(outputPath, img); err != nil {
		return err
	}

	if _, err := exec.LookPath(r.binary); err != nil {
		return fmt.Errorf("matrix binary not found (%s): %w", r.binary, err)
	}

	args := append([]string{}, r.args...)
	args = append(args, outputPath)
	cmd := exec.Command(r.binary, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w (%s)", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func writePNG(path string, img image.Image) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}

func renderHappyEye(width, height int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	bg := color.RGBA{0, 0, 0, 255}
	white := color.RGBA{255, 255, 255, 255}
	black := color.RGBA{0, 0, 0, 255}
	accent := color.RGBA{255, 220, 70, 255}

	fillRect(img, bg)

	cx, cy := float64(width)/2, float64(height)/2
	outerRadius := math.Min(float64(width), float64(height))*0.42
	innerRadius := outerRadius * 0.95
	pupilRadius := outerRadius * 0.24
	highlightRadius := pupilRadius * 0.28

	fillCircle(img, cx, cy, outerRadius, accent)
	fillCircle(img, cx, cy, innerRadius, white)
	fillCircle(img, cx, cy, pupilRadius, black)
	fillCircle(img, cx-pupilRadius*0.35, cy-pupilRadius*0.35, highlightRadius, white)

	// smiling eyelid arc
	arcY := cy + outerRadius*0.15
	for x := 0; x < width; x++ {
		xf := float64(x) - cx
		y := arcY + 0.0035*xf*xf
		for t := -1.2; t <= 1.2; t += 0.3 {
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
