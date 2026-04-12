# Changelog

All notable changes to this project will be documented in this file.

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
