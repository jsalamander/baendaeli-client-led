package main

import (
	"testing"
	"time"
)

func TestNormalizeEmotion(t *testing.T) {
	if got := normalizeEmotion("JOY", "happy"); got != "happy" {
		t.Fatalf("expected happy, got %q", got)
	}
	if got := normalizeEmotion("angry", "happy"); got != "happy" {
		t.Fatalf("expected fallback happy, got %q", got)
	}
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

func TestLoadConfigAnimationOverride(t *testing.T) {
	t.Setenv("POLL_INTERVAL_SECONDS", "30")
	t.Setenv("ANIMATION_SECONDS", "5")

	cfg := loadConfig()
	if cfg.AnimationTime != 5*time.Second {
		t.Fatalf("expected animation time override, got %s", cfg.AnimationTime)
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
