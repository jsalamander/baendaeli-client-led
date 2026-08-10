package main

import (
	"testing"
)

func TestFindEmotionNested(t *testing.T) {
	payload := map[string]any{
		"data": map[string]any{
			"tamagotchi": map[string]any{
				"emotion": "happy",
			},
		},
	}

	emotion, ok := findEmotion(payload)
	if !ok {
		t.Fatalf("expected emotion to be found")
	}
	if emotion != "happy" {
		t.Fatalf("expected happy, got %q", emotion)
	}
}

func TestNormalizeEmotion(t *testing.T) {
	if got := normalizeEmotion("JOY", "happy"); got != "happy" {
		t.Fatalf("expected happy, got %q", got)
	}
	if got := normalizeEmotion("angry", "happy"); got != "happy" {
		t.Fatalf("expected fallback happy, got %q", got)
	}
}

func TestRenderHappyEyeImageSize(t *testing.T) {
	img := renderHappyEye(64, 64)
	b := img.Bounds()
	if b.Dx() != 64 || b.Dy() != 64 {
		t.Fatalf("unexpected image size: %dx%d", b.Dx(), b.Dy())
	}
}
