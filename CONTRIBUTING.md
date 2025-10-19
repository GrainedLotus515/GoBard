# Contributing to GoBard

Thank you for considering contributing to GoBard! This document provides guidelines and instructions for contributing.

## Code of Conduct

- Be respectful and inclusive
- Provide constructive feedback
- Focus on what is best for the community
- Show empathy towards other community members

## How Can I Contribute?

### Reporting Bugs

Before creating bug reports, please check existing issues to avoid duplicates. When creating a bug report, include:

- **Clear title and description**
- **Steps to reproduce** the issue
- **Expected behavior** vs **actual behavior**
- **Environment details** (OS, Go version, etc.)
- **Logs** if applicable

### Suggesting Enhancements

Enhancement suggestions are tracked as GitHub issues. When creating an enhancement suggestion, include:

- **Clear title and description**
- **Use case** - why would this be useful?
- **Possible implementation** if you have ideas

### Pull Requests

1. **Fork the repository** and create your branch from `main`
2. **Make your changes** following the coding standards below
3. **Test your changes** thoroughly
4. **Update documentation** if needed
5. **Commit your changes** with clear commit messages
6. **Push to your fork** and submit a pull request

## Development Setup

1. **Install dependencies:**
   ```bash
   # Install Go 1.21+
   # Install FFmpeg
   # Install yt-dlp
   ```

2. **Clone your fork:**
   ```bash
   git clone https://github.com/YOUR_USERNAME/gobard.git
   cd gobard
   ```

3. **Install Go dependencies:**
   ```bash
   go mod download
   ```

4. **Create `.env` file:**
   ```bash
   cp .env.example .env
   # Edit .env with your test credentials
   ```

5. **Run the bot:**
   ```bash
   go run ./cmd/gobard
   ```

## Coding Standards

### Go Style

- Follow [Effective Go](https://golang.org/doc/effective_go.html)
- Use `gofmt` to format your code
- Run `go vet` before committing
- Add comments for exported functions and types

### Code Organization

```
internal/
├── bot/       # Discord bot logic
├── cache/     # Caching system
├── config/    # Configuration
├── player/    # Music player
├── spotify/   # Spotify integration
└── youtube/   # YouTube integration
```

### Naming Conventions

- **Packages:** lowercase, single word when possible
- **Files:** lowercase with underscores (e.g., `cache_manager.go`)
- **Functions:** CamelCase for exported, camelCase for unexported
- **Variables:** descriptive names, avoid single letters except in loops

### Error Handling

- Always handle errors explicitly
- Wrap errors with context using `fmt.Errorf("context: %w", err)`
- Log errors appropriately
- Don't panic unless absolutely necessary

### Comments

- Document all exported functions, types, and constants
- Use complete sentences
- Explain *why*, not just *what*

Example:
```go
// GetPlayer retrieves the player for a guild, creating one if it doesn't exist.
// This ensures each guild has its own isolated playback state.
func (m *Manager) GetPlayer(guildID string) *GuildPlayer {
    // ...
}
```

## Testing

- Write tests for new functionality
- Ensure existing tests pass: `go test ./...`
- Aim for good test coverage on critical paths

## Commit Messages

Write clear, concise commit messages:

```
Add volume normalization feature

- Implement FFmpeg volume filter
- Add configuration option
- Update documentation
```

Format:
- **First line:** Brief summary (50 chars or less)
- **Blank line**
- **Body:** Detailed explanation if needed (wrap at 72 chars)

## Pull Request Process

1. **Update documentation** for any changed functionality
2. **Add tests** for new features
3. **Ensure CI passes** (if applicable)
4. **Request review** from maintainers
5. **Address feedback** in a timely manner

### PR Title Format

- `feat: Add new feature`
- `fix: Fix bug description`
- `docs: Update documentation`
- `refactor: Refactor code`
- `test: Add or update tests`
- `chore: Maintenance tasks`

## Areas for Contribution

Looking for where to start? Here are some ideas:

### High Priority
- Improve audio playback quality
- Better error handling and user feedback
- Performance optimizations
- Test coverage

### Features
- Additional music sources
- Playlist management improvements
- Advanced queue features
- Web dashboard

### Documentation
- Code examples
- Tutorial videos
- Troubleshooting guides
- API documentation

### Infrastructure
- CI/CD pipeline
- Automated testing
- Docker optimizations
- Monitoring and logging

## Questions?

Feel free to open an issue with the `question` label if you need help or clarification on anything.

## License

By contributing, you agree that your contributions will be licensed under the MIT License.

---

Thank you for contributing to GoBard! 🎵
