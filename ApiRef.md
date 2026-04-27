# Clipboard River Server API Reference

This document describes the current server behavior implemented in this repository.

## Overview

Clipboard River Server uses `HTTP + WebSocket`:

- `HTTP` handles registration, profile updates, heartbeat, text upload, file upload, blob download, and admin pages.
- `WebSocket` handles realtime delivery to online devices.

v1 is designed for one admin-managed account with multiple client devices.

Important delivery rules:

- All accepted clipboard items are stored first.
- Cross-device sync is realtime-only.
- There is no client API to pull another device's historical clipboard items.
- If a target device is offline when a realtime item is uploaded, that missed item is not replayed later.
- Text is delivered directly inside the WebSocket event.
- File items are delivered as metadata only; clients download the binary later with `GET /api/v1/client/items/{id}/blob`.
- All non-text uploads use `content_kind = "file"`.
- Files whose `mime_type` starts with `image/` can still be previewed by clients and the admin UI.

## Runtime And Bootstrap

- Default config path: `./config.json`
- Override config path with `CBR_CONFIG`
- If the target config file does not exist, the server creates it automatically
- `config.example.json` is a sample file only; runtime does not load it unless you point `CBR_CONFIG` to it

First startup behavior:

- the server creates the default account if needed
- the server creates the admin user if needed
- the admin password is generated randomly with 10 characters
- only the bcrypt hash is stored in the database
- the plaintext password is printed once to the startup log

Default start command:

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\go-local.ps1 run ./cmd/cb-river-server
```

## Configuration

Current JSON structure:

```json
{
  "server": {
    "listen_addr": ":8080"
  },
  "storage": {
    "driver": "sqlite",
    "dsn": "./data/clipboard_river.db",
    "data_dir": "./data",
    "blob_dir": "./data/blobs"
  },
  "auth": {
    "session_secret": "replace-with-a-long-random-secret"
  },
  "sync": {
    "default_retention_days": 30,
    "file_max_bytes": 5242880,
    "text_batch_limit": 200
  },
  "admin": {
    "username": "admin"
  }
}
```

Supported environment overrides:

- `CBR_CONFIG`
- `CBR_LISTEN_ADDR`
- `CBR_DB_DRIVER`
- `CBR_DB_DSN`
- `CBR_DATA_DIR`
- `CBR_BLOB_DIR`
- `CBR_SESSION_SECRET`
- `CBR_ADMIN_USERNAME`
- `CBR_RETENTION_DAYS`
- `CBR_FILE_MAX_BYTES`
- `CBR_TEXT_BATCH_LIMIT`

## Identity And Authentication

### Device Identity

- `device_uuid`: stable client-side installation identity
- `device_id`: server-side numeric primary key
- `device_token`: long-lived credential returned by registration

If the same `device_uuid` registers again:

- the existing device row is reused
- mutable metadata is updated
- a fresh `device_token` is issued
- the device is re-enabled if it had been disabled before

If a device is deleted from the admin console:

- its device row is removed from the database
- its current token stops working for both HTTP and WebSocket auth
- historical clipboard items remain in the database
- history pages show that source as a deleted device
- the same `device_uuid` can register again later with a usable enrollment code

### Enrollment Codes

A code is usable only when all of the following are true:

- not revoked
- not expired
- still has remaining uses

Revoking an enrollment code blocks future registrations with that code, but does not affect devices that already registered successfully.

### HTTP Authentication

Authenticated client APIs accept:

```http
Authorization: Bearer <device_token>
```

Fallback:

- `?device_token=<device_token>`

### WebSocket Authentication

The client connects first, then sends a `hello` frame containing:

- `device_token`
- `device_uuid`
- `last_cursor`

`last_cursor` is only used for acknowledgement bookkeeping. The server does not replay missed clipboard items from it.

## Data Model Summary

Main tables:

- `accounts`
- `admin_users`
- `enrollment_tokens`
- `devices`
- `clipboard_items`

Clipboard item deduplication:

- unique key: `(source_device_id, client_item_id)`

This means retries do not require full-table scans; the database uses the unique index for lookup and conflict handling.

## Client API

Base path: `/api/v1/client`

### Endpoint Summary

| Method | Path | Auth | Purpose |
| --- | --- | --- | --- |
| `POST` | `/api/v1/client/register` | No | Register a device with an enrollment code |
| `POST` | `/api/v1/client/device/profile` | Yes | Update mutable device metadata |
| `POST` | `/api/v1/client/heartbeat` | Yes | Refresh `last_seen_at` and fetch runtime settings |
| `POST` | `/api/v1/client/items/text` | Yes | Upload one text clipboard item |
| `POST` | `/api/v1/client/items/text/batch` | Yes | Upload a batch of text items |
| `POST` | `/api/v1/client/items/file` | Yes | Upload one file clipboard item |
| `GET` | `/api/v1/client/items/{id}/blob` | Yes or admin session | Download a file blob |
| `GET` | `/api/v1/client/ws` | WebSocket hello auth | Receive realtime events |

There is no `/sync/pull` endpoint in the current server.

### `POST /api/v1/client/register`

Registers a device and returns a long-lived device token.

Request:

```json
{
  "device_uuid": "stable-install-uuid",
  "enrollment_code": "admin-generated-code",
  "nickname": "My Phone",
  "os_name": "Android",
  "os_version": "15",
  "platform": "mobile",
  "app_version": "1.0.0",
  "capabilities": {
    "supports_text": true,
    "supports_file": true,
    "supports_ws": true,
    "supports_text_batch": true
  }
}
```

Response:

```json
{
  "device_id": 2,
  "device_token": "long-lived-device-token",
  "settings": {
    "realtime_fanout_enabled": true,
    "retention_days": 30,
    "file_max_bytes": 5242880,
    "text_batch_limit": 200
  },
  "server_cursor": 0
}
```

Notes:

- `device_uuid` and `enrollment_code` are required
- the server does not return `ws_url`
- clients derive the WebSocket endpoint from the user-entered server address and connect to `/api/v1/client/ws`

### `POST /api/v1/client/device/profile`

Updates mutable device metadata.

Request:

```json
{
  "nickname": "Office Laptop",
  "os_name": "Windows",
  "os_version": "11 24H2",
  "platform": "desktop",
  "app_version": "1.2.3",
  "capabilities": {
    "supports_text": true,
    "supports_file": true,
    "supports_ws": true,
    "supports_text_batch": true
  }
}
```

Response:

```json
{
  "ok": true
}
```

### `POST /api/v1/client/heartbeat`

Refreshes `last_seen_at` and returns the current runtime settings snapshot.

Response:

```json
{
  "ok": true,
  "server_at": "2026-04-10T08:00:00Z",
  "settings": {
    "realtime_fanout_enabled": true,
    "retention_days": 30,
    "file_max_bytes": 5242880,
    "text_batch_limit": 200
  }
}
```

Notes:

- useful for polling server-side settings
- not used for online status in the admin page
- online status depends on an active WebSocket connection

### `POST /api/v1/client/items/text`

Uploads one text clipboard item.

Request:

```json
{
  "client_item_id": "device-local-stable-item-id",
  "upload_kind": "realtime",
  "client_created_at": "2026-04-09T10:20:30Z",
  "text_content": "hello world"
}
```

Response:

```json
{
  "created": true,
  "item": {
    "id": 12,
    "server_cursor": 12,
    "source_device_id": 2,
    "client_item_id": "device-local-stable-item-id",
    "content_kind": "text",
    "upload_kind": "realtime",
    "mime_type": "",
    "byte_size": 11,
    "char_count": 11,
    "client_created_at": "2026-04-09T10:20:30Z",
    "received_at": "2026-04-10T08:00:00Z",
    "text_content": "hello world"
  }
}
```

Notes:

- `upload_kind` supports `realtime` and `history`
- if omitted, `upload_kind` defaults to `realtime`
- repeated uploads with the same `client_item_id` return the existing row with `"created": false`

### `POST /api/v1/client/items/text/batch`

Uploads a batch of text items, typically for local history backfill from the same device.

Request:

```json
{
  "items": [
    {
      "client_item_id": "hist-1",
      "upload_kind": "history",
      "client_created_at": "2026-04-09T10:20:30Z",
      "text_content": "first"
    },
    {
      "client_item_id": "hist-2",
      "upload_kind": "history",
      "client_created_at": "2026-04-09T10:21:00Z",
      "text_content": "second"
    }
  ]
}
```

Response:

```json
{
  "results": [
    {
      "client_item_id": "hist-1",
      "created": true,
      "item": {
        "id": 13,
        "server_cursor": 13,
        "source_device_id": 2,
        "client_item_id": "hist-1",
        "content_kind": "text",
        "upload_kind": "history",
        "mime_type": "",
        "byte_size": 5,
        "char_count": 5,
        "client_created_at": "2026-04-09T10:20:30Z",
        "received_at": "2026-04-10T08:01:00Z",
        "text_content": "first"
      }
    },
    {
      "client_item_id": "",
      "created": false,
      "error": "client_item_id is required"
    }
  ]
}
```

Notes:

- per-item validation allows partial success
- if batch size exceeds `text_batch_limit`, the whole request is rejected with `400`
- `history` items are stored but are not fanned out in realtime

### `POST /api/v1/client/items/file`

Uploads one file clipboard item with `multipart/form-data`.

Form fields:

| Field | Required | Notes |
| --- | --- | --- |
| `client_item_id` | Yes | Stable per source device |
| `upload_kind` | No | `realtime` or `history`, defaults to `realtime` |
| `client_created_at` | No | RFC3339 timestamp |
| `mime_type` | No | Optional hint |
| `file` | Yes | Exactly one file |

Response:

```json
{
  "created": true,
  "item": {
    "id": 14,
    "server_cursor": 14,
    "source_device_id": 2,
    "client_item_id": "file-1",
    "content_kind": "file",
    "upload_kind": "realtime",
    "mime_type": "text/plain; charset=utf-8",
    "byte_size": 4096,
    "char_count": 0,
    "client_created_at": "2026-04-09T10:22:00Z",
    "received_at": "2026-04-10T08:02:00Z",
    "blob_name": "notes.txt"
  }
}
```

Notes:

- the upload must contain exactly one file
- the server enforces `file_max_bytes` as the single-file size limit
- oversized uploads are rejected with `400`
- the original multipart filename is preserved when possible and returned later as `blob_name`
- the server does not return `blob_url`; clients download later with `GET /api/v1/client/items/{id}/blob`
- the server detects the MIME type from file bytes
- all non-text files are stored with `content_kind = "file"`
- files whose `mime_type` starts with `image/` remain previewable
- repeated uploads with the same `client_item_id` are deduplicated

### `GET /api/v1/client/items/{id}/blob`

Downloads the raw binary content of a file clipboard item.

Behavior:

- authenticated devices can download blobs in the same account
- authenticated admin sessions can also download blobs from the admin UI
- response body is binary, not JSON
- response includes `Content-Disposition: inline`
- when a filename is available, the server also includes it in `Content-Disposition`

Clients should use the item metadata for structured fields such as:

- `mime_type`
- `byte_size`
- `blob_name`

Download URL rule:

- use the returned item `id`
- request `GET /api/v1/client/items/{id}/blob`

## WebSocket Protocol

Endpoint:

```text
GET /api/v1/client/ws
```

Client derivation:

- `http://host:port` -> `ws://host:port/api/v1/client/ws`
- `https://host:port` -> `wss://host:port/api/v1/client/ws`

### First Message: `hello`

The first client frame must be:

```json
{
  "type": "hello",
  "payload": {
    "device_token": "device-token",
    "device_uuid": "stable-install-uuid",
    "last_cursor": 14
  }
}
```

Success response:

```json
{
  "type": "hello_ack",
  "payload": {
    "device_id": 2,
    "settings": {
      "realtime_fanout_enabled": true,
      "retention_days": 30,
      "file_max_bytes": 5242880,
      "text_batch_limit": 200
    },
    "time": "2026-04-10T08:03:00Z"
  }
}
```

### Server Event: `clipboard.created`

Realtime text example:

```json
{
  "type": "clipboard.created",
  "item": {
    "id": 15,
    "server_cursor": 15,
    "source_device_id": 3,
    "client_item_id": "text-15",
    "content_kind": "text",
    "upload_kind": "realtime",
    "mime_type": "",
    "byte_size": 11,
    "char_count": 11,
    "client_created_at": "2026-04-09T10:23:00Z",
    "received_at": "2026-04-10T08:04:00Z",
    "text_content": "hello world"
  }
}
```

Realtime file example with previewable `mime_type`:

```json
{
  "type": "clipboard.created",
  "item": {
    "id": 16,
    "server_cursor": 16,
    "source_device_id": 3,
    "client_item_id": "file-16",
    "content_kind": "file",
    "upload_kind": "realtime",
    "mime_type": "image/png",
    "byte_size": 102400,
    "char_count": 0,
    "client_created_at": "2026-04-09T10:24:00Z",
    "received_at": "2026-04-10T08:05:00Z",
    "blob_name": "clip-123-origin name.png"
  }
}
```

For file events, the client then downloads the binary with:

```text
GET /api/v1/client/items/16/blob
```

Realtime generic file example:

```json
{
  "type": "clipboard.created",
  "item": {
    "id": 17,
    "server_cursor": 17,
    "source_device_id": 3,
    "client_item_id": "file-17",
    "content_kind": "file",
    "upload_kind": "realtime",
    "mime_type": "application/pdf",
    "byte_size": 204800,
    "char_count": 0,
    "client_created_at": "2026-04-09T10:25:00Z",
    "received_at": "2026-04-10T08:06:00Z",
    "blob_name": "manual.pdf"
  }
}
```

### Client Message: `ack`

```json
{
  "type": "ack",
  "payload": {
    "server_cursor": 16
  }
}
```

The server stores the acknowledged cursor in `devices.last_acked_cursor`.

### Client Message: `ping`

```json
{
  "type": "ping"
}
```

Server response:

```json
{
  "type": "pong"
}
```

The server also sends WebSocket ping frames internally to keep the connection healthy.

## Realtime Fanout Rules

A clipboard item is pushed over WebSocket only when all of the following are true:

- account-level realtime fanout is enabled
- the source device has `send_realtime_enabled = true`
- the target device has `receive_realtime_enabled = true`
- the target device is not disabled
- the item `upload_kind` is `realtime`
- the target device is currently online through an active WebSocket connection
- the target device is not the same as the source device

If any condition fails, the item is still stored, but no realtime push happens.

## Admin Console

Embedded admin pages:

- `/admin/login`
- `/admin/history`
- `/admin/devices`
- `/admin/tokens`
- `/admin/settings`

Current admin capabilities:

- server-side paginated history browsing
- filter by device, content kind, and text query
- file download plus preview when `mime_type` starts with `image/`
- device online status based on active WebSocket connections
- toggle device send permission
- toggle device receive permission
- disable or re-enable a device
- delete a device record
- create and revoke enrollment codes
- update retention days
- update global realtime fanout switch
- update file size limit
- change admin password

Current history page size:

- 50 records per page

Password notes:

- password changes require the current password
- minimum new password length is 8

## Retention And Cleanup

- `retention_days = 0` means keep data forever
- otherwise, the server sets `expires_at` on creation
- a cleanup job runs every hour
- expired rows are deleted
- expired blob files are deleted from disk

Maintenance script:

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\clear-history.ps1
```

Options:

- `-Force` skips confirmation
- `-ConfigPath <path>` uses a custom config path

What it clears:

- all `clipboard_items`
- all entries under the configured blob directory

What it does not clear:

- devices
- enrollment tokens
- admin users

## Docker

Build:

```powershell
docker build -t clipboard-river-server .
```

Compose:

```bash
docker compose up -d --build
```

Compose defaults:

- compose file: `./compose.yaml`
- host data directory: `./data`
- `CBR_CONFIG=./data/config.json`
- config, SQLite database, and blob files are stored under `/app/data`

Run:

```powershell
docker run --name clipboard-river-server -p 8080:8080 -v ${PWD}\data:/app/data clipboard-river-server
```

Container behavior:

- `CBR_CONFIG=./data/config.json`
- config, SQLite database, and blob files are all stored under `/app/data`
- if `/app/data/config.json` is missing, the server creates it automatically
- the first generated admin password is printed to container logs

## Development Notes

- default database: SQLite through GORM with `github.com/glebarez/sqlite`
- optional drivers: MySQL and PostgreSQL
- run tests with `./scripts/go-local.ps1 test ./...`
- build with `./scripts/go-local.ps1 build ./...`
