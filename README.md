# WayDesk

Low-latency remote desktop for Wayland, built with Go.

WayDesk uses the **XDG Desktop Portal** (D-Bus) for secure screen capture negotiation and **PipeWire** for zero-copy frame delivery — the only officially supported path under Wayland's security model.

## Architecture

```
┌────────────────────────────────────────────────┐
│                   WayDesk                      │
│                                                │
│  cmd/waydesk/       ← Entry point              │
│  internal/portal/   ← D-Bus portal client      │
│                                                │
│  (planned)                                     │
│  internal/capture/  ← PipeWire frame consumer  │
│  internal/encode/   ← GPU encoding (NVENC)     │
│  internal/net/      ← WebRTC (pion/webrtc)     │
│  internal/input/    ← libei / uinput           │
└────────────────────────────────────────────────┘
```

### Current Implementation (Phase 1)

The initial skeleton handles the XDG Desktop Portal ScreenCast handshake:

1. **D-Bus session** — Connects to the session bus via `godbus/dbus/v5`
2. **CreateSession** — Establishes a portal ScreenCast session
3. **SelectSources** — Triggers the compositor's screen picker dialog
4. **Start** — Begins the cast; retrieves PipeWire stream node IDs
5. **OpenPipeWireRemote** — Obtains the PipeWire file descriptor for frame access

## Prerequisites

Arch Linux packages:

```bash
sudo pacman -S xdg-desktop-portal pipewire go
# Plus one backend for your compositor:
# GNOME:     xdg-desktop-portal-gnome
# KDE:       xdg-desktop-portal-kde
# Hyprland:  xdg-desktop-portal-hyprland
# wlroots:   xdg-desktop-portal-wlr
```

## Build & Run

```bash
make build    # → bin/waydesk
make run      # Build + run
make vet      # Static analysis
make fmt      # Format code
make clean    # Remove artifacts
```

### Usage

```bash
./bin/waydesk                          # Default: text logs, 60s timeout
./bin/waydesk -log-format json         # JSON structured logs
./bin/waydesk -timeout 30s             # Custom portal timeout
```

When launched, WayDesk will:
1. Open your compositor's screen sharing dialog
2. Print the PipeWire node ID(s) and FD
3. Wait for `Ctrl+C`, then close the session cleanly

## Roadmap

| Phase | Component | Description |
|-------|-----------|-------------|
| ✅ 1 | Portal | XDG Desktop Portal ScreenCast handshake |
| 🔲 2 | Capture | PipeWire frame consumer via `libpipewire` (cgo) |
| 🔲 3 | Encode | GPU encoding — NVENC / VA-API via FFmpeg |
| 🔲 4 | Network | WebRTC P2P via `pion/webrtc` |
| 🔲 5 | Input | Remote input simulation — `libei` / `uinput` |

## License

MIT
