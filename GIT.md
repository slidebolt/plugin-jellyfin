# Git Workflow for plugin-jellyfin

Layer 7 plugin. It depends on `sb-contract`, `sb-domain`, `sb-media-sdk`,
`sb-messenger-sdk`, `sb-runtime`, and `sb-storage-sdk`.

Validate with `go test ./...`, `go vet ./...`, and
`go build ./cmd/plugin-jellyfin`. Release tags publish Linux amd64 and arm64
binaries. Never commit Jellyfin URLs, tokens, device IDs, user names, or live
server responses.
