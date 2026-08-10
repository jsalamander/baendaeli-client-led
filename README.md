# baendaeli-client-led

LED eye emotion client for Baendae.li.

## What it does

- Polls `https://www.baendae.li/api/public/tamagotchi` on an interval
- Reads an emotion field from the JSON payload
- Renders a single 64x64 eye on an RGB matrix panel
- Currently supports one expression: `happy`
- Runs as a systemd service on Raspberry Pi

## Hardware target

- 64x64 RGB Matrix LED Panel
- RGB Matrix HAT + RTC for Raspberry Pi (Mini Kit)

## Configuration

The binary is configured through environment variables:

- `BAENDAELI_URL` (default: `https://www.baendae.li/api/public/tamagotchi`)
- `POLL_INTERVAL_SECONDS` (default: `30`)
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

Update later with:

```bash
curl -fsSL https://jsalamander.github.io/baendaeli-client-led/update_pi.sh | sudo bash
```

## Releases

Tagged releases (`v*`) trigger the release workflow to:

1. Build Linux `amd64` and `arm64` binaries
2. Publish release artifacts
3. Generate installer scripts and publish them via GitHub Pages
