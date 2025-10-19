# Voice Connection Setup Guide

## Error 4016: Unknown Encryption Mode

If you're seeing this error:
```
voice endpoint websocket closed unexpectantly, websocket: close 4016: Unknown encryption mode.
```

This means the required encryption libraries are not installed on your system.

## Required System Libraries

GoBard requires these system libraries for Discord voice connections:

1. **libopus** - Opus audio codec
2. **libsodium** - Encryption library (NaCl/libsodium)

## Installation Instructions

### Arch Linux (your system)

```bash
sudo pacman -S opus libsodium
```

### Debian/Ubuntu

```bash
sudo apt update
sudo apt install libopus-dev libsodium-dev
```

### Fedora/RHEL

```bash
sudo dnf install opus-devel libsodium-devel
```

### macOS

```bash
brew install opus libsodium
```

### Alpine Linux (for Docker)

```bash
apk add opus-dev libsodium-dev
```

## Verification

After installing, verify the libraries are available:

```bash
# Check for opus
pkg-config --modversion opus

# Check for libsodium
pkg-config --modversion libsodium
```

## Rebuild After Installing

After installing the system libraries, rebuild GoBard:

```bash
cd /home/lotus/Gitea/GoBard
go clean
go build ./cmd/gobard
```

## Why These Libraries Are Needed

- **libopus**: Discord requires Opus codec for all voice audio. This provides high-quality, low-latency audio compression.

- **libsodium**: Discord uses XSalsa20-Poly1305 encryption (via libsodium/NaCl) to encrypt all voice data. This is required for the voice connection handshake.

## Testing Voice Connection

Once the libraries are installed and you've rebuilt:

1. Start the bot: `./gobard`
2. Join a voice channel in Discord
3. Use `/play <song>`
4. The bot should join and play audio without the 4016 error

## Troubleshooting

### Still getting 4016 error after installing libraries?

1. **Rebuild the bot completely**:
   ```bash
   go clean -cache
   go build ./cmd/gobard
   ```

2. **Check library paths**:
   ```bash
   ldconfig -p | grep libopus
   ldconfig -p | grep libsodium
   ```

3. **Verify Go can find the libraries**:
   ```bash
   pkg-config --cflags --libs opus
   pkg-config --cflags --libs libsodium
   ```

### Docker Users

If using Docker, the Dockerfile needs to be updated to include these libraries. See the updated Dockerfile in the repository.

### Different Encryption Error Codes

- **4014**: Disconnected manually (normal)
- **4015**: Voice server crashed
- **4016**: Unknown encryption mode (missing libsodium)

## Quick Fix for Your System (Arch Linux)

```bash
# Install the required libraries
sudo pacman -S opus libsodium

# Rebuild GoBard
cd /home/lotus/Gitea/GoBard
go clean
go build ./cmd/gobard

# Run the bot
./gobard
```

The error should be resolved after this!
