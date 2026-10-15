# Provider capabilities

## Product requirement confirmed 2026-10-15

Music playback and controls must remain in Discord, with audio delivered through
Discord voice. A companion browser page or external official player does not
satisfy this requirement. Official-player controls or metadata alone must not be
presented as an alternative Discord audio backend.

The user reports persistent CAPTCHA rejection from the current VPS IP. Treat
that as an external deployment constraint, not a defect the rewrite can promise
to fix. Local playback has the recorded successful tests below; no inference is
made that another host will guarantee provider availability. Alternative backends
must establish both usable audio acquisition and authorization for the intended
delivery. Browser capture, personal cookies and restriction bypasses are not a
solution within the approved scope. A separate worker remains a proposal, not an
implemented workaround or an authorization to alter infrastructure.

## Capability matrix

| Provider/backend | Metadata | Audio acquisition | Official player | Discord delivery | Status |
| --- | --- | --- | --- | --- | --- |
| YouTube/yt-dlp | Search/video/playlist | Conditional on provider availability and permitted use | Separate browser embedding | Through decode/Opus pipeline | Live audio/recovery and successful listening rerun verified; source resets remain possible |
| Local test media | Fixture metadata | Caller-owned bytes | None | Through tested codec/output boundary | Real FFmpeg/Opus slice verified offline; not a public path input |
| Spotify | Historical track/playlist bridge | No supported raw full-track backend established | SDK/player-control is a separate capability | Not established | Assessed; not enabled |
| Self-hosted HTTP | Possible | Requires explicit origin/redirect policy | None required | Technically possible | Proposed only |

YouTube authentication/anti-bot rejection is an external terminal condition.
Rewriting internals cannot guarantee successful extraction. Personal cookies,
undocumented capture and bypasses are not baseline requirements.

On 2026-10-08, a local `/play hope` search produced audible Discord audio and
2,233 accepted frames before yt-dlp exited with a connection reset. A separate
paced extraction reproduced the same safe diagnostic fingerprint and errno 104.
The optional cache download contained a complete 110.961-second Opus file
(5,548 frames checked with ffprobe). That verifies acquisition of this test item,
not provider-wide reliability or completed live delivery. A finite source-resume
policy is tested offline; plain search retains the first-result selection behavior.

On 2026-10-09, another live request resumed once after a connection reset at
203.980 seconds and completed the remaining 64.560 seconds of accepted audio.
This verifies the recovery path in live use; the user's qualified listening
report does not establish uninterrupted audibility. See
[the recorded evidence and resource caveats](rewrite/live-2026-10-09.md).

The October 12 rerun was reported successful by the user. It still required one
resume after a source reset at 180.540 seconds, then completed; no measured
read/send calls exceeded 60 ms. [The rerun evidence](rewrite/live-2026-10-12.md)
records exact timing semantics and does not attribute earlier stutter to gaming.

An independent official-player wrapper would expose device/session selection,
authorized play/pause/seek, state and errors. It would not imply a PCM reader or
Discord rebroadcast permission. Build that library separately if approved.

## Assessment (2026-10-07)

Spotify's [Web Playback SDK reference](https://developer.spotify.com/documentation/web-playback-sdk/reference)
describes a browser player with authenticated Premium-user tokens and
play/pause/seek/state controls. It does not expose a server PCM acquisition
contract. Its policy notes restrict broadcasting. The
[developer policy](https://developer.spotify.com/policy) imposes additional
streaming/content restrictions. Inference: a Go wrapper around official player
control is technically distinct from, and does not establish authorization or
capability for, Discord audio delivery. No capture/rebroadcast backend is built.

The [February 2026 API changelog](https://developer.spotify.com/documentation/web-api/references/changes/february-2026)
documents changed endpoint/access behavior. Historical client-credentials
playlist code must not be restored assuming its old access still works.
Metadata restoration remains credential/entitlement-dependent and unverified.

YouTube's [service terms](https://www.youtube.com/static?template=terms) restrict
downloading and automated access except where authorized. The existing
extractor is retained as a conditional integration, not a claim of general
provider permission. A future official API integration also has distinct
[developer policies](https://developers.google.com/youtube/terms/developer-policies).
No authentication/rate-limit bypass, personal-cookie default or silent fallback
is introduced.

Additional feasible backend: operator-owned media can supply real bytes to the
same decoder without provider credentials, subject to the operator's rights.
The implemented fixture path accepts no Discord-provided filesystem path.
A public self-hosted URL backend remains proposed: require configured origins,
DNS/address validation at connection time, redirect revalidation, byte/time
limits and credential isolation. Broad arbitrary URL support is not enabled.
