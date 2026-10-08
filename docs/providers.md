# Provider capabilities

| Provider/backend | Metadata | Audio acquisition | Official player | Discord delivery | Status |
| --- | --- | --- | --- | --- | --- |
| YouTube/yt-dlp | Search/video/playlist | Conditional on provider availability and permitted use | Separate browser embedding | Through decode/Opus pipeline | Existing backend; live verification pending |
| Local test media | Fixture metadata | Caller-owned bytes | None | Through tested codec/output boundary | Reference slice under construction; not a public path input |
| Spotify | Historical track/playlist bridge | No supported raw full-track backend established | SDK/player-control is a separate capability | Not established | Assessment pending; not enabled |
| Self-hosted HTTP | Possible | Requires explicit origin/redirect policy | None required | Technically possible | Proposed only |

YouTube authentication/anti-bot rejection is an external terminal condition.
Rewriting internals cannot guarantee successful extraction. Personal cookies,
undocumented capture and bypasses are not baseline requirements.

An independent official-player wrapper would expose device/session selection,
authorized play/pause/seek, state and errors. It would not imply a PCM reader or
Discord rebroadcast permission. Build that library separately if approved.

Provider terms and technical documentation must be assessed before enabling a
new backend. This table makes no claim of independently verified Spotify audio.
