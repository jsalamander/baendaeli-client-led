package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"io"
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
	"golang.org/x/sys/unix"
)

const (
	defaultAPIURL           = "https://www.baendae.li/api/public/tamagotchi"
	defaultPollInterval     = 2 * time.Second
	emotionLoopMaxDuration  = defaultPollInterval - 40*time.Millisecond
	defaultMatrixBinary     = "led-image-viewer"
	defaultMatrixMapping    = "adafruit-hat"
	defaultSoundBinary      = "aplay"
	defaultLocalSoundDevice = "default"
	defaultProdSoundDevice  = "plughw:1,0"
	eyelashCount            = 5
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
	binary            string
	args              []string
	currentEmotion    string
	currentCmd        *exec.Cmd
	currentCancel     context.CancelFunc
	currentDone       <-chan struct{}
	currentPaths      []string
	warnedUnavailable bool
}

func resolveSoundBinary(binary string) string {
	for _, candidate := range []string{binary, "aplay", "ffplay", "play", "speaker-test"} {
		if candidate == "" {
			continue
		}
		if _, err := exec.LookPath(candidate); err == nil {
			return candidate
		}
	}
	return binary
}

func (p *beepPlayer) stop() error {
	if p.currentCancel == nil {
		return nil
	}
	p.currentCancel()
	if p.currentDone != nil {
		<-p.currentDone
	} else if p.currentCmd != nil {
		_ = p.currentCmd.Wait()
	}
	for _, path := range p.currentPaths {
		_ = os.Remove(path)
	}
	p.currentEmotion = ""
	p.currentCmd = nil
	p.currentCancel = nil
	p.currentDone = nil
	p.currentPaths = nil
	return nil
}

func (p *beepPlayer) isPlaying(emotion string) bool {
	if p.currentEmotion != emotion || p.currentCancel == nil || p.currentDone == nil {
		return false
	}
	select {
	case <-p.currentDone:
		return false
	default:
		return true
	}
}

func (p *beepPlayer) PlayEmotion(emotion string) error {
	emotion = normalizeEmotion(emotion, "happy")
	if emotion == "" {
		emotion = "happy"
	}
	p.binary = resolveSoundBinary(p.binary)
	if _, err := exec.LookPath(p.binary); err != nil {
		if !p.warnedUnavailable {
			log.Printf("sound binary unavailable (%s): %v; install alsa-utils for aplay or configure SOUND_BINARY", p.binary, err)
			p.warnedUnavailable = true
		}
		return nil
	}
	if p.isPlaying(emotion) {
		return nil
	}
	if err := p.stop(); err != nil {
		return err
	}
	variantSamples := emotionLoopVariants(emotion)
	outputPaths := make([]string, 0, len(variantSamples))
	for _, samples := range variantSamples {
		path, err := os.CreateTemp("", "baendaeli-client-led-sound-*.wav")
		if err != nil {
			for _, outputPath := range outputPaths {
				_ = os.Remove(outputPath)
			}
			return err
		}
		outputPath := path.Name()
		if err := path.Close(); err != nil {
			_ = os.Remove(outputPath)
			return err
		}
		if err := writeWAV(outputPath, samples); err != nil {
			_ = os.Remove(outputPath)
			for _, previousPath := range outputPaths {
				_ = os.Remove(previousPath)
			}
			return err
		}
		outputPaths = append(outputPaths, outputPath)
	}

	outputPath := outputPaths[0]
	cmdArgs := append([]string{}, p.args...)
	if p.binary == "ffplay" || p.binary == "play" {
		cmdArgs = []string{"-nodisp", "-autoexit", "-loop", "0", outputPath}
		if p.binary == "play" {
			cmdArgs = []string{"-q", outputPath, "repeat", "0"}
		}
	} else if p.binary == "aplay" {
		cmdArgs = aplayArgs(p.args, outputPath)
	} else if p.binary == "speaker-test" {
		cmdArgs = speakerTestArgs(p.args, emotionBaseFrequency(emotion))
		_ = os.Remove(outputPath)
		outputPath = ""
	}
	ctx, cancel := context.WithCancel(context.Background())
	if p.binary == "aplay" {
		cmdArgsList := make([][]string, 0, len(outputPaths))
		for _, outputPath := range outputPaths {
			cmdArgsList = append(cmdArgsList, aplayArgs(p.args, outputPath))
		}
		done := make(chan struct{})
		go func() {
			defer close(done)
			for ctx.Err() == nil {
				for _, args := range cmdArgsList {
					cmd := exec.CommandContext(ctx, p.binary, args...)
					if err := cmd.Run(); err != nil {
						if ctx.Err() == nil {
							log.Printf("sound playback failed (%s): %v", p.binary, err)
						}
						return
					}
					if ctx.Err() != nil {
						return
					}
				}
			}
		}()
		p.currentEmotion = emotion
		p.currentCancel = cancel
		p.currentDone = done
		p.currentPaths = outputPaths
		return nil
	}
	cmd := exec.CommandContext(ctx, p.binary, cmdArgs...)
	if err := cmd.Start(); err != nil {
		cancel()
		for _, outputPath := range outputPaths {
			_ = os.Remove(outputPath)
		}
		return err
	}
	p.currentEmotion = emotion
	p.currentCmd = cmd
	p.currentCancel = cancel
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	p.currentDone = done
	p.currentPaths = outputPaths
	return nil
}

func aplayArgs(args []string, outputPath string) []string {
	result := append([]string{}, args...)
	return append(result, "-q", outputPath)
}

func speakerTestArgs(args []string, frequency int) []string {
	result := make([]string, 0, len(args)+4)
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch arg {
		case "-q", "--quiet":
			continue
		case "-f", "--frequency", "-l", "--loop", "-t", "--test":
			index++
			continue
		}
		if strings.HasPrefix(arg, "--frequency=") || strings.HasPrefix(arg, "--loop=") || strings.HasPrefix(arg, "--test=") {
			continue
		}
		result = append(result, arg)
	}
	return append(result, "-t", "sine", "-f", strconv.Itoa(frequency), "-l", "0")
}

func emotionBaseFrequency(emotion string) int {
	switch emotion {
	case "excited":
		return 880
	case "sad":
		return 220
	case "calm":
		return 392
	case "sleep":
		return 174
	default:
		return 659
	}
}

func main() {
	previewEmotion := flag.String("preview", "", "write a GIF preview for an emotion")
	previewOutput := flag.String("output", "", "GIF preview output path")
	soundPreviewEmotion := flag.String("sound-preview", "", "write a WAV preview for an emotion")
	soundPreviewOutput := flag.String("sound-output", "", "WAV preview output path")
	manualMode := flag.Bool("manual", false, "interactive manual emotion selector")
	manualEmotion := flag.String("emotion", "", "manual emotion to render and play immediately")
	flag.Parse()
	if *previewEmotion != "" {
		if err := writePreview(*previewEmotion, *previewOutput); err != nil {
			log.Fatalf("preview failed: %v", err)
		}
	}
	if *soundPreviewEmotion != "" {
		if err := writeSoundPreview(*soundPreviewEmotion, *soundPreviewOutput); err != nil {
			log.Fatalf("sound preview failed: %v", err)
		}
	}
	if *previewEmotion != "" || *soundPreviewEmotion != "" {
		return
	}

	cfg := loadConfig()
	if *manualMode || *manualEmotion != "" {
		if *manualEmotion == "" {
			if err := runManualEmotionMode(cfg, ""); err != nil {
				log.Fatalf("manual mode failed: %v", err)
			}
			return
		}
		if emotion, ok := parseManualEmotion(*manualEmotion); ok {
			if err := runManualEmotionMode(cfg, emotion); err != nil {
				log.Fatalf("manual emotion failed: %v", err)
			}
			return
		}
		log.Fatalf("invalid manual emotion %q", *manualEmotion)
	}
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
			if err := beeper.PlayEmotion(normalized); err != nil {
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

func parseManualEmotion(raw string) (string, bool) {
	normalized := normalizeEmotion(raw, "")
	if normalized == "" {
		return "", false
	}
	switch normalized {
	case "happy", "excited", "sad", "calm", "sleep":
		return normalized, true
	default:
		return "", false
	}
}

func runManualEmotionMode(cfg config, initial string) error {
	emotions := []string{"happy", "excited", "sad", "calm", "sleep"}
	current := initial
	if current == "" {
		current = emotions[0]
	}

	r := &matrixRenderer{
		binary: cfg.MatrixBinary,
		args:   cfg.MatrixArgs,
		anim:   cfg.AnimationTime,
		width:  cfg.Width,
		height: cfg.Height,
	}
	beeper := &beepPlayer{binary: cfg.SoundBinary, args: cfg.SoundArgs}
	defer func() { _ = beeper.stop() }()
	reader := bufio.NewReader(os.Stdin)
	isTTY := isTerminal(int(os.Stdin.Fd()))
	var restoreInput func()
	if isTTY {
		var err error
		restoreInput, err = makeInputImmediate(int(os.Stdin.Fd()))
		if err != nil {
			return err
		}
		defer restoreInput()
	}

	showCurrent := func() error {
		if err := beeper.PlayEmotion(current); err != nil {
			return err
		}
		if err := r.Display(current); err != nil {
			return err
		}
		fmt.Printf("active emotion: %s\n", current)
		return nil
	}
	if err := showCurrent(); err != nil {
		return err
	}

	for {
		fmt.Println("Choose emotion: happy, excited, sad, calm, sleep, or q to quit")
		if isTTY {
			key, err := readManualKey()
			if err != nil {
				if err == io.EOF {
					return nil
				}
				return err
			}
			switch key {
			case "q", "Q":
				return nil
			case "up":
				current = cycleEmotion(emotions, current, -1)
			case "down":
				current = cycleEmotion(emotions, current, 1)
			case "enter":
				continue
			default:
				if emotion, ok := parseManualEmotion(key); ok {
					current = emotion
				}
			}
			if err := showCurrent(); err != nil {
				return err
			}
			continue
		}
		input, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		choice := strings.TrimSpace(strings.ToLower(input))
		switch choice {
		case "q", "quit", "exit":
			return nil
		case "":
			continue
		default:
			emotion, ok := parseManualEmotion(choice)
			if !ok {
				fmt.Printf("unknown emotion: %q\n", choice)
				continue
			}
			current = emotion
			if err := showCurrent(); err != nil {
				return err
			}
		}
	}
}

func cycleEmotion(emotions []string, current string, delta int) string {
	for i, emotion := range emotions {
		if emotion == current {
			idx := (i + delta + len(emotions)) % len(emotions)
			return emotions[idx]
		}
	}
	return emotions[0]
}

func readManualKey() (string, error) {
	buf := make([]byte, 3)
	if _, err := os.Stdin.Read(buf[:1]); err != nil {
		return "", err
	}
	if buf[0] != 0x1b {
		return string(buf[0]), nil
	}
	if _, err := os.Stdin.Read(buf[1:3]); err != nil {
		return "", err
	}
	if len(buf) >= 3 && buf[1] == '[' {
		switch buf[2] {
		case 'A':
			return "up", nil
		case 'B':
			return "down", nil
		}
	}
	return "", nil
}

func isTerminal(fd int) bool {
	_, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	return err == nil
}

func makeInputImmediate(fd int) (func(), error) {
	original, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		return nil, err
	}

	immediate := *original
	immediate.Lflag &^= unix.ICANON
	immediate.Cc[unix.VMIN] = 1
	immediate.Cc[unix.VTIME] = 0
	if err := unix.IoctlSetTermios(fd, unix.TCSETS, &immediate); err != nil {
		return nil, err
	}
	return func() { _ = unix.IoctlSetTermios(fd, unix.TCSETS, original) }, nil
}

func writeSoundPreview(emotion, outputPath string) error {
	emotion = normalizeEmotion(emotion, "happy")
	if outputPath == "" {
		outputPath = filepath.Join("previews", emotion+".wav")
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return err
	}
	if err := writeWAV(outputPath, emotionPlaylistSamples(emotion)); err != nil {
		return err
	}
	log.Printf("wrote %s sound preview to %s", emotion, outputPath)
	return nil
}

func writeWAV(path string, samples []int16) error {
	const sampleRate = 22050
	const bitsPerSample = 16
	const channels = 1
	dataLen := len(samples) * 2
	buf := make([]byte, 44+dataLen)
	copy(buf[0:4], "RIFF")
	binary.LittleEndian.PutUint32(buf[4:8], uint32(36+dataLen))
	copy(buf[8:12], "WAVE")
	copy(buf[12:16], "fmt ")
	binary.LittleEndian.PutUint32(buf[16:20], 16)
	binary.LittleEndian.PutUint16(buf[20:22], 1)
	binary.LittleEndian.PutUint16(buf[22:24], channels)
	binary.LittleEndian.PutUint32(buf[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(buf[28:32], uint32(sampleRate*channels*bitsPerSample/8))
	binary.LittleEndian.PutUint16(buf[32:34], uint16(channels*bitsPerSample/8))
	binary.LittleEndian.PutUint16(buf[34:36], bitsPerSample)
	copy(buf[36:40], "data")
	binary.LittleEndian.PutUint32(buf[40:44], uint32(dataLen))
	for i, sample := range samples {
		binary.LittleEndian.PutUint16(buf[44+i*2:44+i*2+2], uint16(sample))
	}
	return os.WriteFile(path, buf, 0o644)
}

func emotionLoopSamples(emotion string) []int16 {
	return emotionLoopVariants(emotion)[0]
}

func emotionPlaylistSamples(emotion string) []int16 {
	variants := emotionLoopVariants(emotion)
	length := 0
	for _, samples := range variants {
		length += len(samples)
	}
	playlist := make([]int16, 0, length)
	for _, samples := range variants {
		playlist = append(playlist, samples...)
	}
	return playlist
}

func emotionLoopVariants(emotion string) [][]int16 {
	const sampleRate = 22050
	type composition struct {
		phrases  [][]int
		duration float64
		warmth   float64
	}
	compositions := map[string]composition{
		"happy":   {[][]int{{659, 783, 880, 988}, {783, 880, 1046, 988}, {659, 880, 988, 1174}, {1046, 988, 880, 783}}, 0.15, 0.18},
		"excited": {[][]int{{880, 1046, 1174, 1318, 1174, 1046}, {988, 1174, 1396, 1174, 1318, 1568}, {880, 1174, 1318, 1568, 1396, 1174}, {1046, 1318, 1568, 1760, 1568, 1318}}, 0.10, 0.08},
		"sad":     {[][]int{{220, 196, 174, 196}, {220, 196, 174, 147}, {196, 174, 164, 147}, {174, 196, 174, 147}}, 0.15, 0.30},
		"calm":    {[][]int{{392, 440, 523, 440}, {392, 349, 392, 440}, {523, 440, 392, 349}, {392, 440, 392, 330}}, 0.15, 0.38},
		"sleep":   {[][]int{{220, 196, 174, 147}, {196, 174, 147, 131}, {174, 147, 131, 147}, {147, 174, 196, 174}}, 0.15, 0.48},
	}
	selected, ok := compositions[emotion]
	if !ok {
		selected = compositions["happy"]
	}

	variants := make([][]int16, 3)
	for variant := range variants {
		var samples []int16
		for phraseIndex := 0; phraseIndex < 3; phraseIndex++ {
			phrase := selected.phrases[(phraseIndex+variant)%len(selected.phrases)]
			for noteIndex, frequency := range phrase {
				noteSamples := int(selected.duration * sampleRate)
				for sampleIndex := 0; sampleIndex < noteSamples; sampleIndex++ {
					progress := float64(sampleIndex) / float64(noteSamples)
					envelope := math.Min(1, progress*14) * math.Min(1, (1-progress)*9)
					timePosition := float64(len(samples)) / sampleRate
					wave := math.Sin(2*math.Pi*float64(frequency)*timePosition)*(1-selected.warmth) + math.Sin(4*math.Pi*float64(frequency)*timePosition)*selected.warmth
					if emotion == "excited" && (noteIndex+variant)%2 == 0 {
						wave += 0.12 * math.Sin(6*math.Pi*float64(frequency)*timePosition)
					}
					samples = append(samples, int16(10500*envelope*wave))
				}
			}
		}
		fadeSamples := int(0.04 * sampleRate)
		for index := 0; index < fadeSamples && index < len(samples); index++ {
			gain := float64(index) / float64(fadeSamples)
			samples[index] = int16(float64(samples[index]) * gain)
			samples[len(samples)-1-index] = int16(float64(samples[len(samples)-1-index]) * gain)
		}
		variants[variant] = samples
	}
	return variants
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
		"--led-rows=64", "--led-cols=64", "--led-chain=1", "--led-parallel=1",
		fmt.Sprintf("--led-gpio-mapping=%s", defaultMatrixMapping), "--led-pixel-mapper=Rotate:90",
		"--led-brightness=60", "--led-no-drop-privs",
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

	soundArgs := []string{}
	if raw := strings.TrimSpace(os.Getenv("SOUND_DEVICE")); raw != "" {
		soundArgs = append([]string{"-D", raw})
	} else if isProductionEnv() {
		soundArgs = []string{"-D", defaultProdSoundDevice}
	} else {
		soundArgs = []string{"-D", defaultLocalSoundDevice}
	}
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

func isProductionEnv() bool {
	for _, key := range []string{"APP_ENV", "ENVIRONMENT", "PRODUCTION", "PROD"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			switch strings.ToLower(value) {
			case "1", "true", "yes", "on", "production", "prod":
				return true
			}
		}
	}
	return false
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
		log.Printf("matrix binary unavailable (%s): %v; skipping display", r.binary, err)
		return nil
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

	cx := float64(width) / 2
	outerRadius := math.Min(float64(width), float64(height)) * 0.42
	lidY := float64(height)*0.88 + outerRadius*offset
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
		for step := 0; step < 8; step++ {
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
	drawUpperLashes(img, cx, cy, outerRadius, blink, 0.66, 0.72, -0.005, 0, 4, accent)
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
