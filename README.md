# SlideBolt Jellyfin Plugin

Jellyfin media integration for SlideBolt. It exposes Jellyfin servers,
libraries, catalog items, artwork, active sessions, and remotely controllable
players through SlideBolt's provider-neutral media contracts.

The plugin resolves original media streams directly from Jellyfin. Tokens are
used only for authenticated requests and generated stream URLs; they are never
stored in entities or written to logs. Jellyfin Live TV and DVR are outside the
first release.

## Configuration

- `JELLYFIN_URL` — Jellyfin server base URL.
- `JELLYFIN_TOKEN` — Jellyfin API token.
- `JELLYFIN_REFRESH_INTERVAL` — inventory/session refresh interval (default `30s`).

Quickstart obtains these values from the plugin's serialized setup definition.
