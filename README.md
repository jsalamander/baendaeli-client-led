# baendaeli-client-led

LED eye emotion client for Baendae.li.

## What it does

- Polls `https://www.baendae.li/api/public/tamagotchi` on an interval
- Reads an emotion field from the JSON payload
- Renders a looping 64x64 animated eye on an RGB matrix panel
- Supports `happy`, `excited`, `sad`, `calm`, and `sleep` expressions
- Shows a `NETWORK ERROR` message on the panel when the API cannot be reached
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
- `MATRIX_ARGS` (optional; defaults set for 64x64 panel + Adafruit HAT mapping, including a 90-degree clockwise correction)
- `SOUND_ENABLED` (default: `true`; set to `false` to disable emotion audio)
- `SOUND_BINARY` (default: `aplay`)
- `SOUND_DEVICE` (optional ALSA device, such as `plughw:2,0`)
- `SOUND_ARGS` (optional arguments for `SOUND_BINARY`)

You can place these in `/opt/baendaeli-client-led/.env` for systemd.

## Local development

```bash
go test ./...
go build ./...
```

To hear the generated emotion loops in `--manual` mode on Linux, install the
ALSA utilities, which provide the default `aplay` audio backend:

```bash
sudo apt-get install alsa-utils
```

Then build and run the local debug binary:

```bash
make debug
./baendaeli-client-led-debug --manual
```

## GIF previews

Generate the same 64x64 looping GIF used by the LED client, without requiring
the panel:

```bash
make preview EMOTION=excited
```

The GIF is written to `previews/excited.gif`. Valid emotions are `happy`,
`excited`, `sad`, `calm`, and `sleep`. Open the generated GIF in VS Code or a browser to
review animation changes. Set `OUTPUT` to choose another path.

## Raspberry Pi install

```bash
curl -fsSL https://jsalamander.github.io/baendaeli-client-led/install_pi.sh | sudo bash
```

The installer also builds and installs `led-image-viewer` from
`hzeller/rpi-rgb-led-matrix` into `/usr/local/bin` and installs `alsa-utils`
for the beep player.
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
