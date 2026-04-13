# Changelog

All notable changes to this project will be documented in this file.

## [0.2.1.0] - 2026-04-13

### Added
- Chinese-first console copy across the main panel flow, including connection, node list, unclaimed nodes, config editor, task progress, and add-node command modal
- Focused frontend regression coverage for localized node/task status, event, and task-type translation paths

### Changed
- Refactored console status/event/task-type translation into a dedicated frontend helper module so the localized state mapping is easier to maintain and test
- Docker release pipeline now publishes multi-architecture images for both `linux/amd64` and `linux/arm64`

### Fixed
- Agent install script now escapes double-quoted YAML values before writing `agent.yaml`, preventing installs from breaking on names or paths containing quotes or backslashes
- Server install script now generates random tokens without tripping `set -euo pipefail`, so fresh installs don't die on token creation
- Web panel dates and relative-time labels now render in Chinese instead of falling back to English phrasing

## [0.2.0.0] - 2026-04-12

### Added
- Nezha-style one-click agent install: panel generates per-node install commands with embedded secrets
- Universal install command for bulk fleet onboarding (nodes appear as "unclaimed")
- Node claiming flow: unclaimed nodes can be named and activated from the Web UI
- Pre-create nodes in Web UI with "Add Node" button, get a ready-to-paste install command
- Server one-click deployment script (`install-server.sh`) with binary + systemd setup
- Docker support: Dockerfile and docker-compose.yml for containerized deployment
- GitHub Actions release workflow: auto-builds binaries and Docker image on tag push
- `external_url` server config for NAT/reverse proxy environments
- Server YAML config file support (`-config` flag) alongside CLI flags

### Changed
- Agent now supports `install_secret` for pre-assigned node registration
- Agent clears `install_secret` from config after successful registration (one-time use)
- Install script fully rewritten with server-side parameter injection, auto-detection of XrayR/V2bX configs, and input validation

### Fixed
- Add-node dialog now prefills display name field correctly
