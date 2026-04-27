<p align="center">
  <img src="Assets/ClipboardRiverLogo.transparent.png" alt="Clipboard River logo" width="128" />
</p>

<h1 align="center">Clipboard River</h1>

<p align="center">
  <a href="README.md">English</a> | <a href="README.zh-CN.md">简体中文</a>
</p>

<p align="center">
  A fast, low-friction clipboard sync system focused on making cross-device copy and paste feel almost invisible.
</p>

## What It Feels Like

Clipboard River is built around one simple goal: clipboard sync should feel invisible.

When both devices are on Windows, the ideal experience is:

1. Copy on one device with `Ctrl + C`
2. Switch to the other device
3. Paste immediately with `Ctrl + V`

No manual refresh, no extra send button, no "open this panel first" mental overhead.

That seamless experience is the core value of the project.

## Why Clipboard River

- Fast: realtime delivery with very little delay
- Low-friction: designed to feel like the clipboard is simply shared
- Practical: supports text and single-file clipboard sync
- Controlled: self-hosted server with device management and history browsing
- Cross-platform: Windows is the smoothest experience today, Android is already available, and iOS is planned for a future version

## Platform Status

### Windows

Windows is the best experience today.

With both Windows clients online, Clipboard River can feel almost native: copy on one side, paste on the other side right away.

### Android

Android is supported, but the experience is naturally a bit more involved because of Android platform restrictions around clipboard access and background behavior.

It still works well, but it is not as effortless as the Windows-to-Windows flow.

### iOS

iOS is planned for a future version.

If you want to build your own client before then, use [ApiRef.md](./ApiRef.md) as the protocol reference.

## Current Limitations

- The best seamless experience today is still Windows to Windows while both devices are online.
- On Windows, file-copy capture currently triggers only when exactly one file is copied.
- Multi-file copy and folder copy are not supported yet.
- Non-text clipboard sync currently uses a single-file upload path; files whose MIME starts with `image/` get inline preview in the admin UI.
- Rich text, HTML fragments, and complex Office-style clipboard payloads are not supported yet.
- Android works, but it is naturally less frictionless than the Windows-to-Windows flow because of platform restrictions.

## This Repository

This repository contains the server:

- device registration
- realtime message fanout
- clipboard history storage
- file blob storage
- admin console
- Docker-friendly deployment

Protocol and API details live in [ApiRef.md](./ApiRef.md). The README intentionally stays focused on product value and getting started.

## Repositories

- Server: `cb_river_server` (this repository)
- Windows client: [cb_river_client_windows](https://github.com/CoronaAustralis/ClipboardRiverWindowsClient)
- Android client: [cb_river_client_android](https://github.com/CoronaAustralis/ClipboardRiverAndroidClient)
- API reference: [ApiRef.md](./ApiRef.md)

Client repository URLs above follow the current project naming used in this workspace. If you publish them under different names or organizations, update the links here.

## Quick Start

1. Start the server:

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\go-local.ps1 run ./cmd/cb-river-server
```

2. If `config.json` does not exist, the server creates it automatically. Set `CBR_CONFIG` if you want a different path.
3. On first startup, the server creates the admin account if needed, stores the password hash in the database, and prints the generated 10-character password once in the log.
4. Open [http://127.0.0.1:8080/admin/login](http://127.0.0.1:8080/admin/login).
5. Generate an enrollment code in the admin UI and register your clients.

`config.example.json` is only a sample file. The runtime default is `./config.json`.

## Docker

### Docker Compose

```bash
docker compose up -d --build
```

- compose file: `./compose.yaml`
- host data directory: `./data`
- container config path: `/app/data/config.json`
- config, SQLite database, and blob files all live under `/app/data`
- the first generated admin password is printed to container logs

### Docker Run

```
docker run --name clipboard-river-server -p 8080:8080 -v ${PWD}\data:/app/data crestfallmax/clipboard-river-server
```

- container config path: `/app/data/config.json`
- config, SQLite database, and blob files all live under `/app/data`
- the first generated admin password is printed to container logs

## Docs And Utilities

- API reference: [ApiRef.md](./ApiRef.md)
- example config: [config.example.json](./config.example.json)
- clear clipboard history and blob files: `./scripts/clear-history.ps1`
- run tests: `./scripts/go-local.ps1 test ./...`
