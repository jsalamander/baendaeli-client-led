package main

import (
	"context"
	"image"
	"image/color"
	"image/gif"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestNormalizeEmotion(t *testing.T) {
	if got := normalizeEmotion("JOY", "happy"); got != "happy" {
		t.Fatalf("expected happy, got %q", got)
	}
	if got := normalizeEmotion("sadness", "happy"); got != "sad" {
		t.Fatalf("expected sad, got %q", got)
	}
	if got := normalizeEmotion("excited", "happy"); got != "excited" {
		t.Fatalf("expected excited, got %q", got)
	}
	if got := normalizeEmotion("calm", "happy"); got != "calm" {
		t.Fatalf("expected calm, got %q", got)
	}
	if got := normalizeEmotion("sleepy", "happy"); got != "sleep" {
		t.Fatalf("expected sleep, got %q", got)
	}
	if got := normalizeEmotion("angry", "happy"); got != "happy" {
		t.Fatalf("expected fallback happy, got %q", got)
	}
}

func TestEyelashCount(t *testing.T) {
	if eyelashCount != 5 {
		t.Fatalf("expected exactly five eyelashes, got %d", eyelashCount)
	}
}

func TestRenderNetworkErrorMessage(t *testing.T) {
	anim := renderTextAnimation("NETWORK ERROR", 64, 64)
	if len(anim.Image) != 1 || len(anim.Delay) != 1 {
		t.Fatalf("expected one looping text frame")
	}
	if anim.Image[0].Bounds().Dx() != 64 || anim.Image[0].Bounds().Dy() != 64 {
		t.Fatalf("unexpected network error frame size: %v", anim.Image[0].Bounds())
	}
	if anim.Image[0].ColorIndexAt(32, 28) == 0 {
		t.Fatal("expected visible network error text")
	}
}
func TestFetchEmotionFromTopLevelResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"emotion":"sad"}`))
	}))
	defer server.Close()

	emotion, err := fetchEmotion(context.Background(), server.Client(), server.URL)
	if err != nil {
		t.Fatalf("fetchEmotion returned an error: %v", err)
	}
	if emotion != "sad" {
		t.Fatalf("expected sad, got %q", emotion)
	}
}

func TestWritePreview(t *testing.T) {
	outputPath := filepath.Join(t.TempDir(), "calm.gif")
	if err := writePreview("calm", outputPath); err != nil {
		t.Fatalf("writePreview returned an error: %v", err)
	}
	file, err := os.Open(outputPath)
	if err != nil {
		t.Fatalf("open preview: %v", err)
	}
	defer file.Close()
	if _, err := gif.DecodeAll(file); err != nil {
		t.Fatalf("decode preview GIF: %v", err)
	}
}

func TestWriteSoundPreview(t *testing.T) {
	outputPath := filepath.Join(t.TempDir(), "sleep.wav")
	if err := writeSoundPreview("sleep", outputPath); err != nil {
		t.Fatalf("writeSoundPreview returned an error: %v", err)
	}
	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read sound preview: %v", err)
	}
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		t.Fatalf("expected RIFF/WAVE content, got %q", data[:12])
	}
}

func TestDisplaySkipsWhenMatrixBinaryMissing(t *testing.T) {
	r := &matrixRenderer{binary: "missing-matrix-binary", args: nil, width: 64, height: 64}
	if err := r.Display("happy"); err != nil {
		t.Fatalf("expected missing matrix binary to be ignored in headless mode, got %v", err)
	}
}

func TestPlayEmotionWithoutSoundBinaryIsSilent(t *testing.T) {
	player := &beepPlayer{binary: "definitely-missing-binary"}
	if err := player.PlayEmotion("happy"); err != nil {
		t.Fatalf("expected missing sound binary to be ignored, got %v", err)
	}
}

func TestSpeakerTestArgsKeepsConfiguredDeviceAndDropsQuietFlag(t *testing.T) {
	got := speakerTestArgs([]string{"-q", "-D", "plughw:2,0", "-t", "sine", "-f", "880", "-l", "1"}, 174)
	want := []string{"-D", "plughw:2,0", "-t", "sine", "-f", "174", "-l", "0"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("speakerTestArgs() = %q, want %q", got, want)
	}
}

func TestAplayArgsKeepsConfiguredDevice(t *testing.T) {
	got := aplayArgs([]string{"-D", "plughw:2,0"}, "emotion.wav")
	want := []string{"-D", "plughw:2,0", "-q", "--loop=0", "emotion.wav"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("aplayArgs() = %q, want %q", got, want)
	}
}

func TestEmotionLoopSamplesFadeAtBoundary(t *testing.T) {
	samples := emotionLoopSamples("sleep")
	if len(samples) < 1000 {
		t.Fatalf("loop too short: %d", len(samples))
	}
	first := absInt16(samples[0])
	last := absInt16(samples[len(samples)-1])
	if first > 200 || last > 200 {
		t.Fatalf("expected loop boundary to fade to zero, got first=%d last=%d", first, last)
	}
}

func absInt16(v int16) int {
	if v < 0 {
		return int(-v)
	}
	return int(v)
}

func TestRenderHappyEyeAnimation(t *testing.T) {
	anim := renderHappyEyeAnimation(64, 64)
	if len(anim.Image) == 0 {
		t.Fatalf("expected at least one animation frame")
	}
	if len(anim.Image) != len(anim.Delay) {
		t.Fatalf("frame and delay lengths differ: %d vs %d", len(anim.Image), len(anim.Delay))
	}
	b := anim.Image[0].Bounds()
	if b.Dx() != 64 || b.Dy() != 64 {
		t.Fatalf("unexpected frame size: %dx%d", b.Dx(), b.Dy())
	}
}

func TestRenderSadEyeAnimation(t *testing.T) {
	anim := renderSadEyeAnimation(64, 64)
	if len(anim.Image) == 0 {
		t.Fatalf("expected at least one animation frame")
	}
	if len(anim.Image) != len(anim.Delay) {
		t.Fatalf("frame and delay lengths differ: %d vs %d", len(anim.Image), len(anim.Delay))
	}
	b := anim.Image[0].Bounds()
	if b.Dx() != 64 || b.Dy() != 64 {
		t.Fatalf("unexpected frame size: %dx%d", b.Dx(), b.Dy())
	}
}

func TestDrawEyebrowAddsBlackPixels(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	fillRect(img, color.RGBA{255, 255, 255, 255})
	drawEyebrow(img, 32, 8, 20, 0.004, 0.035, color.RGBA{0, 0, 0, 255})

	foundBlack := false
	for y := 0; y < 20 && !foundBlack; y++ {
		for x := 0; x < 64; x++ {
			if img.RGBAAt(x, y) == (color.RGBA{0, 0, 0, 255}) {
				foundBlack = true
				break
			}
		}
	}
	if !foundBlack {
		t.Fatal("expected eyebrow to draw black pixels")
	}
}

func TestFillLowerEyelidMasksBottomOfEye(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	fillRect(img, color.RGBA{255, 255, 255, 255})
	fillLowerEyelid(img, 32, 32, 26, 0, 0.0035, 0)

	if got := img.RGBAAt(32, 55); got != (color.RGBA{0, 0, 0, 255}) {
		t.Fatalf("expected lower eyelid to mask the bottom of the eye, got %#v", got)
	}
}

func TestExcitedEyebrowIsVisible(t *testing.T) {
	anim := renderExcitedEyeAnimation(64, 64)
	if got := color.RGBAModel.Convert(anim.Image[0].At(32, 8)); got != (color.RGBA{185, 35, 100, 255}) {
		t.Fatalf("expected a dark-pink eyebrow pixel at the top of the excited eye, got %#v", got)
	}
}

func TestRenderSleepEyeAnimation(t *testing.T) {
	anim := renderSleepEyeAnimation(64, 64)
	if len(anim.Image) == 0 {
		t.Fatalf("expected at least one animation frame")
	}
	if len(anim.Image) != len(anim.Delay) {
		t.Fatalf("frame and delay lengths differ: %d vs %d", len(anim.Image), len(anim.Delay))
	}
	b := anim.Image[0].Bounds()
	if b.Dx() != 64 || b.Dy() != 64 {
		t.Fatalf("unexpected frame size: %dx%d", b.Dx(), b.Dy())
	}
}

func TestManualEmotionChoice(t *testing.T) {
	for input, want := range map[string]string{
		"happy":      "happy",
		"  sleepy  ": "sleep",
		"SAD":        "sad",
		"calm":       "calm",
		"quit":       "",
		"hello":      "",
	} {
		got, ok := parseManualEmotion(input)
		if want == "" {
			if ok {
				t.Fatalf("expected %q to be rejected, got %q", input, got)
			}
			continue
		}
		if !ok || got != want {
			t.Fatalf("parseManualEmotion(%q) = %q, %v; want %q", input, got, ok, want)
		}
	}
}

func TestSleepLashesExtendFartherThanSadLashes(t *testing.T) {
	sleep := renderSleepEyeFrame(64, 64, 0)
	sad := renderSadEyeFrame(64, 64, 0, 0)

	if maxPinkY(sleep) <= maxPinkY(sad) {
		t.Fatalf("expected sleep lashes to extend farther than sad lashes: sleep=%d sad=%d", maxPinkY(sleep), maxPinkY(sad))
	}
}

func maxPinkY(img *image.RGBA) int {
	maxY := -1
	for y := 0; y < img.Bounds().Dy(); y++ {
		for x := 0; x < img.Bounds().Dx(); x++ {
			if got := img.RGBAAt(x, y); got == (color.RGBA{185, 35, 100, 255}) {
				if y > maxY {
					maxY = y
				}
			}
		}
	}
	return maxY
}

func TestAllEmotionAnimations(t *testing.T) {
	for _, emotion := range []string{"happy", "excited", "sad", "calm", "sleep"} {
		anim := animationForEmotion(emotion, 64, 64)
		if len(anim.Image) == 0 || len(anim.Image) != len(anim.Delay) {
			t.Fatalf("invalid animation for %q", emotion)
		}
		bounds := anim.Image[0].Bounds()
		if bounds.Dx() != 64 || bounds.Dy() != 64 {
			t.Fatalf("unexpected size for %q: %dx%d", emotion, bounds.Dx(), bounds.Dy())
		}
	}
}

func TestLoadConfigAnimationDefaultsToPollInterval(t *testing.T) {
	t.Setenv("POLL_INTERVAL_SECONDS", "7")
	t.Setenv("ANIMATION_SECONDS", "")

	cfg := loadConfig()
	if cfg.PollInterval != 7*time.Second {
		t.Fatalf("unexpected poll interval: %s", cfg.PollInterval)
	}
	if cfg.AnimationTime != 7*time.Second {
		t.Fatalf("expected animation time to match poll interval, got %s", cfg.AnimationTime)
	}
}

func TestLoadConfigCorrectsPanelRotation(t *testing.T) {
	t.Setenv("MATRIX_ARGS", "")

	cfg := loadConfig()
	foundRotation := false
	for _, arg := range cfg.MatrixArgs {
		if arg == "--led-pixel-mapper=Rotate:90" {
			foundRotation = true
			break
		}
	}
	if !foundRotation {
		t.Fatal("expected default matrix arguments to correct the panel rotation")
	}
}

func TestLoadConfigAnimationOverride(t *testing.T) {
	t.Setenv("POLL_INTERVAL_SECONDS", "30")
	t.Setenv("ANIMATION_SECONDS", "5")

	cfg := loadConfig()
	if cfg.AnimationTime != 5*time.Second {
		t.Fatalf("expected animation time override, got %s", cfg.AnimationTime)
	}
}

func TestLoadConfigSoundEnabledByDefault(t *testing.T) {
	t.Setenv("SOUND_ENABLED", "")

	cfg := loadConfig()
	if !cfg.SoundEnabled {
		t.Fatal("expected sound to be enabled by default")
	}
}

func TestLoadConfigCanDisableSound(t *testing.T) {
	t.Setenv("SOUND_ENABLED", "false")

	cfg := loadConfig()
	if cfg.SoundEnabled {
		t.Fatal("expected sound to be disabled")
	}
}

func TestHasTimingArgs(t *testing.T) {
	if hasTimingArgs([]string{"--led-rows=64"}) {
		t.Fatalf("did not expect timing flags")
	}
	if !hasTimingArgs([]string{"--led-rows=64", "-t", "1.5"}) {
		t.Fatalf("expected -t to be detected")
	}
	if !hasTimingArgs([]string{"--led-rows=64", "-w2"}) {
		t.Fatalf("expected -w prefix to be detected")
	}
	if !hasTimingArgs([]string{"--led-rows=64", "-l1"}) {
		t.Fatalf("expected -l prefix to be detected")
	}
}
