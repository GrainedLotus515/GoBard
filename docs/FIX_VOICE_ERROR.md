# Fix for Voice Error 4016

## The Problem

You're seeing this error:
```
voice endpoint websocket closed unexpectantly, websocket: close 4016: Unknown encryption mode.
```

## The Solution

Discord voice requires two system libraries that are missing:

### Quick Fix (Arch Linux)

```bash
# Install the missing libraries
sudo pacman -S opus libsodium

# Rebuild the bot
cd /home/lotus/Gitea/GoBard
go clean
go build ./cmd/gobard

# Run the bot
./gobard
```

That's it! The bot should now connect to voice channels without the 4016 error.

## Why This Happens

- **libopus**: Required for encoding/decoding Opus audio (Discord's audio codec)
- **libsodium**: Required for XSalsa20-Poly1305 encryption (Discord's voice encryption)

Without these libraries, the bot can't negotiate the proper encryption mode with Discord's voice servers, resulting in error code 4016.

## Verification

After installing, you can verify the libraries are installed:

```bash
# Check opus
pkg-config --modversion opus

# Check libsodium  
pkg-config --modversion libsodium
```

## What I've Already Updated

✅ Updated `go.mod` dependencies to latest versions
✅ Added gopus library for Opus support
✅ Updated Dockerfile to include voice libraries
✅ Updated README with installation instructions
✅ Created VOICE_SETUP.md with detailed troubleshooting

## Next Steps

1. Install the libraries: `sudo pacman -S opus libsodium`
2. Rebuild: `go build ./cmd/gobard`
3. Test: Join a voice channel and use `/play`

The bot should now successfully connect to voice and play audio! 🎵
