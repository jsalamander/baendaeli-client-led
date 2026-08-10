# baendaeli-client-led

LED eye emotion client for Baendae.li.

## What it does

- Polls `https://www.baendae.li/api/public/tamagotchi` on an interval
- Reads an emotion field from the JSON payload
- Renders a looping 64x64 animated eye on an RGB matrix panel
- Currently supports one expression: `happy`
- Runs as a systemd service on Raspberry Pi

## Hardware target

- 64x64 RGB Matrix LED Panel
- RGB Matrix HAT + RTC for Raspberry Pi (Mini Kit)

## Configuration

The binary is configured through environment variables:

- `BAENDAELI_URL` (default: `https://www.baendae.li/api/public/tamagotchi`)
- `POLL_INTERVAL_SECONDS` (default: `30`)
- `ANIMATION_SECONDS` (default: same as `POLL_INTERVAL_SECONDS`)
- `FALLBACK_EMOTION` (default: `happy`)
- `MATRIX_BINARY` (default: `led-image-viewer`)
- `MATRIX_ARGS` (optional; defaults set for 64x64 panel + Adafruit HAT mapping)

You can place these in `/opt/baendaeli-client-led/.env` for systemd.

## Local development

```bash
go test ./...
go build ./...
```

## Raspberry Pi install

```bash
curl -fsSL https://jsalamander.github.io/baendaeli-client-led/install_pi.sh | sudo bash
```

The installer also builds and installs `led-image-viewer` from
`hzeller/rpi-rgb-led-matrix` into `/usr/local/bin`.
The systemd service runs as `root` so matrix GPIO access works reliably.

Update later with:

```bash
curl -fsSL https://jsalamander.github.io/baendaeli-client-led/update_pi.sh | sudo bash
```

## Releases

Tagged releases (`v*`) trigger the release workflow to:

1. Build Linux `amd64` and `arm64` binaries
2. Publish release artifacts
3. Generate installer scripts and publish them via GitHub Pages
