# Setup Verification Guide

This guide helps you verify that GoBard is properly configured and ready to run.

## Step 1: Verify Dependencies

Run these commands to check all dependencies are installed:

```bash
# Check Go version (needs 1.21+)
go version

# Check FFmpeg (needs 4.1+)
ffmpeg -version

# Check yt-dlp
yt-dlp --version
```

Expected output:
- Go: `go version go1.21` or higher
- FFmpeg: `ffmpeg version 4.1` or higher  
- yt-dlp: Version number (e.g., `2024.xx.xx`)

## Step 2: Verify .env File

Create your `.env` file from the example:

```bash
cp .env.example .env
```

Edit `.env` and add at minimum:

```bash
DISCORD_TOKEN=your_actual_discord_bot_token
```

### Test .env Loading

You can verify the .env file is being read correctly:

```bash
# Create a simple test
cat > test_config.sh << 'EOF'
#!/bin/bash
echo "Testing .env file loading..."

# Check if .env exists
if [ ! -f .env ]; then
    echo "❌ .env file not found!"
    echo "Run: cp .env.example .env"
    exit 1
fi

# Check if DISCORD_TOKEN is set
if grep -q "^DISCORD_TOKEN=your_discord_bot_token_here" .env; then
    echo "❌ DISCORD_TOKEN is still set to placeholder!"
    echo "Edit .env and add your actual Discord bot token"
    exit 1
fi

echo "✅ .env file exists and appears configured"
EOF

chmod +x test_config.sh
./test_config.sh
```

## Step 3: Build the Project

```bash
# Download dependencies
go mod download

# Build the project
go build ./cmd/gobard
```

Expected output:
- No errors
- Binary `gobard` created (approximately 11MB)

```bash
# Verify binary was created
ls -lh gobard
```

## Step 4: Run a Configuration Test

The bot will automatically load the `.env` file when it starts. You can test this:

```bash
# Start the bot (it will fail if DISCORD_TOKEN is invalid, but that's expected)
./gobard
```

You should see:
```
Starting GoBard...
Loaded configuration from .env file
```

If you see `No .env file found, using environment variables`, the .env file wasn't found.

## Step 5: Verify Discord Bot Setup

Before running the bot, ensure:

1. ✅ Bot token is copied to `.env` file
2. ✅ Bot has required intents enabled:
   - Message Content Intent
   - Server Members Intent  
   - Presence Intent
3. ✅ Bot is invited to your server with permissions:
   - Read Messages/View Channels
   - Send Messages
   - Embed Links
   - Connect
   - Speak
   - Use Voice Activity
   - Use Slash Commands

## Environment Variable Loading Order

GoBard loads configuration in this order:

1. **First**: Reads `.env` file (if it exists) using `godotenv`
2. **Then**: Reads from system environment variables
3. **Finally**: Uses default values for optional settings

This means:
- You can use `.env` file for local development
- You can use environment variables for Docker/production
- System environment variables override `.env` file values

## Common Issues

### "No .env file found"

**Cause**: The `.env` file doesn't exist in the current directory.

**Solution**:
```bash
cp .env.example .env
nano .env  # Edit and add your Discord token
```

### "DISCORD_TOKEN environment variable is required"

**Cause**: The `.env` file exists but `DISCORD_TOKEN` is empty or still set to placeholder.

**Solution**:
```bash
# Edit .env and set your actual token
nano .env
```

### Bot connects but commands don't appear

**Cause**: Commands take time to register with Discord.

**Solution**:
- Wait 1-5 minutes for guild commands
- Wait up to 1 hour if using `REGISTER_COMMANDS_ON_BOT=true`
- Try kicking and re-inviting the bot

## Successful Start Example

When everything is configured correctly, you'll see:

```
Starting GoBard...
Loaded configuration from .env file
2024/10/18 17:46:00 Bot is now running. Press CTRL-C to exit.
2024/10/18 17:46:01 Logged in as: GoBard#1234
2024/10/18 17:46:02 Registering commands per guild...
2024/10/18 17:46:03 Commands registered successfully
```

## Quick Verification Checklist

- [ ] Go 1.21+ installed
- [ ] FFmpeg installed
- [ ] yt-dlp installed
- [ ] `.env` file created from `.env.example`
- [ ] `DISCORD_TOKEN` set in `.env`
- [ ] Project builds without errors
- [ ] Bot starts and shows "Loaded configuration from .env file"
- [ ] Bot appears online in Discord
- [ ] Slash commands appear in Discord (may take a few minutes)

## Next Steps

Once verification is complete:

1. Join a voice channel in your Discord server
2. Use `/play never gonna give you up` to test
3. Check the logs for any errors
4. See [QUICKSTART.md](QUICKSTART.md) for more commands

## Getting Help

If you're still having issues:

1. Check the error message carefully
2. Review [README.md](README.md) configuration section
3. Verify all dependencies are correctly installed
4. Check Discord Developer Portal for bot configuration
5. Open an issue on GitHub with:
   - Error message
   - Your OS and Go version
   - Steps you've tried

---

Happy music listening! 🎵
