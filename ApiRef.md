# Clipboard River Client API Reference

This document defines the breaking client protocol implemented by this server. See [fix.md](./fix.md) for the implementation checklist and [README.md](./README.md) for deployment and administration. Clients using the previous protocol must be updated.

## Scope And Optional Features

The server and clients are independent projects. A client may implement only the features it needs. There is no feature negotiation or feature declaration field. For example, a text-only client can register and upload text without implementing files, batch uploads, synchronization, or WebSocket. A receiving client can implement WebSocket and optionally synchronize recent text. Ignore unsupported content kinds and unknown server event types.

HTTP handles registration, application metadata/settings, uploads, recent-text synchronization, and file downloads. WebSocket delivers live events. The server currently serves one admin-managed account with multiple devices; all authenticated operations are scoped to that account.

Delivery rules:

- Accepted items are stored before they are returned or announced.
- Realtime text events contain text; file events contain metadata for a separate download.
- The synchronization window contains at most 50 recent **text** items, including both `realtime` and `history` uploads. Returned IDs and contents are filtered by device permissions, so a client may see fewer items.
- “Recent” means `client_created_at` descending, breaking ties by `item_id` descending. A higher ID does not imply a newer clipboard item.
- WebSocket does not replay missed events. Reconnecting clients may reconcile the current text window through HTTP. This does not recover every missed item or any missed file event.
- Leaving the window does not mean an item has been deleted from database history. Clients decide how long to keep their own history.

## Common Definitions

### Identity And Authentication

| Field | Meaning |
| --- | --- |
| `device_uuid` | Stable client-generated installation identity |
| `device_id` | Server-assigned numeric device primary key |
| `device_token` | Long-lived device credential, replaced on every successful registration |
| `item_id` | Numeric database primary key of the clipboard history row; an identity, not a synchronization position |
| `app_name` | Client-defined application name, recorded without driving server behavior |
| `app_version` | Client-defined application version string |

HTTP client authentication:

```http
Authorization: Bearer <device_token>
```

The query parameter `?device_token=<device_token>` is also accepted. Except for registration, every HTTP client endpoint requires a valid token for a device that has not been deleted. A token cannot operate on another account's data. WebSocket uses the `hello` message described below.

Timestamps use RFC3339. Missing `client_created_at` on an upload defaults to the server's receipt time; invalid supplied timestamps are rejected. Comparisons use timestamp instants, regardless of timezone notation. `received_at` is assigned by the server. Clients should provide accurate creation times because those determine window membership.

### Client Settings

Every `settings` object in this protocol has exactly this structure:

```json
{
  "file_max_bytes": 5242880,
  "text_batch_limit": 200
}
```

| Field | Meaning |
| --- | --- |
| `file_max_bytes` | Positive integer: maximum bytes per uploaded file; `0`: no application-level size limit; `-1`: file uploads not accepted |
| `text_batch_limit` | Positive integer: maximum item count per text batch request |

Values below `-1` are invalid for `file_max_bytes`. Disabling file uploads does not prevent downloading retained files. Upload validation uses the settings effective when the server processes the request. Clients can refresh their snapshot through the device profile endpoint; there is no settings version or settings-change event.

### Upload Receipt Versus Delivered Item

A successful upload returns a compact receipt:

```json
{
  "created": true,
  "item": {
    "item_id": 12,
    "content_kind": "text",
    "upload_kind": "realtime",
    "mime_type": "",
    "received_at": "2026-04-10T08:00:00Z"
  }
}
```

This receipt is used by single uploads and successful batch results. It does not echo content, size, creation time, source device, or filename. Match it to the original upload locally.

A **full text item**, used by synchronization responses and realtime events, is:

```json
{
  "item_id": 12,
  "source_device_id": 2,
  "content_kind": "text",
  "upload_kind": "realtime",
  "mime_type": "",
  "byte_size": 11,
  "char_count": 11,
  "client_created_at": "2026-04-09T10:20:30Z",
  "received_at": "2026-04-10T08:00:00Z",
  "text_content": "hello world"
}
```

`byte_size` is the UTF-8 byte count for text, and the binary byte count for files. `char_count` counts Unicode code points for text and is `0` for files. `source_device_id` is the numeric device primary key and remains unchanged when that device is deleted; the server retains the device row. All non-text content uses `content_kind = "file"`; image files are identified by a MIME type starting with `image/`.

Each accepted upload creates a new database row. Retrying the same payload can create a duplicate; the protocol offers no idempotency key or content-based deduplication. A successful single upload always has `created: true`. Clients should avoid blindly retrying an upload whose outcome is unknown.

## Client HTTP API

### Endpoint Summary

| Method | Path | Authentication | Purpose |
| --- | --- | --- | --- |
| `POST` | `/api/v1/client/register` | Enrollment code | Register or replace a device credential |
| `POST` | `/api/v1/client/device/profile` | Device token | Update own application metadata and/or fetch settings |
| `POST` | `/api/v1/client/items/text` | Device token | Upload one text item |
| `POST` | `/api/v1/client/items/text/batch` | Device token | Upload text items with individual results |
| `POST` | `/api/v1/client/items/file` | Device token | Upload one file |
| `POST` | `/api/v1/client/sync/text` | Device token | Inspect/fetch the recent-text window |
| `GET` | `/api/v1/client/items/{item_id}/blob` | Device token | Download a file |
| `GET` | `/api/v1/client/ws` | WebSocket hello | Receive live events |

JSON requests use `Content-Type: application/json`, except the optional empty profile request. File upload uses `multipart/form-data`. JSON examples below show response bodies. Successful HTTP operations return `200`; file downloads return binary. Invalid fields, oversized batches/files, and disabled file uploads return `400` with `{"error":"description"}`. Invalid/revoked credentials or deleted devices return `401`; inaccessible, missing, or non-file blob targets return `404`. Batch item failures are represented inside a successful HTTP response as described below.

### `POST /api/v1/client/register`

Request:

```json
{
  "device_uuid": "stable-install-uuid",
  "enrollment_code": "current-window-code",
  "nickname": "My Phone",
  "app_name": "My Clipboard Client",
  "app_version": "1.0.0"
}
```

`device_uuid` and `enrollment_code` are required nonempty strings. `nickname`, `app_name`, and `app_version` are optional strings. Omitted metadata defaults to an empty string for a new device and preserves existing values on re-registration; supplied values replace previous values.

Response:

```json
{
  "device_id": 2,
  "device_token": "new-long-lived-device-token",
  "settings": {
    "file_max_bytes": 5242880,
    "text_batch_limit": 200
  }
}
```

Repeated registration is allowed. A matching `device_uuid` reuses its device row and `device_id`, updates supplied metadata, clears its deletion flag if needed, and issues a fresh token. Every registration, including re-registration, requires enrollment to be open and `enrollment_code` to match the current code. While enrollment is open, the code has unlimited uses; there is no usage counter. The previous token immediately stops authenticating HTTP or WebSocket connections; already authenticated WebSocket connections using that token are closed. A failed registration must not revoke the existing token. There is no separate device-disable state: turning send/receive off leaves authentication, WebSocket connections, and uploads available, while still applying the synchronization permission rules.

An administrator can open or close enrollment at any time. Each opening generates a new code and opens a fixed five-minute window; opening again replaces the code and starts a fresh five-minute window. Enrollment closes automatically when that window ends. Manual closure immediately invalidates the current code. Enrollment starts closed after every server restart, and previous codes cannot be reused. A closed window or an incorrect/replaced code returns `400` with an error description.

The server checks the active window, current code, and five-minute deadline together when authorizing a registration. A registration authorized before closure may finish afterward; closure prevents new authorizations. Closing enrollment, automatic closure, replacing its code, or restarting the server does not itself revoke already issued device tokens. Deleting a device marks it deleted, revokes its token, and closes its WebSocket sessions. Its database row, `device_id`, and historical `source_device_id` associations are retained. Registering the same UUID again requires the current open window and code, restores that same row and ID, and issues a new token; existing send/receive preferences are preserved. The old token remains invalid.

### `POST /api/v1/client/device/profile`

Combines application metadata updates and settings lookup. The authenticated token determines which device is updated. The only editable fields are `app_name` and `app_version`; supplied fields replace their previous values, omitted fields remain unchanged.

```json
{
  "app_name": "My Clipboard Client",
  "app_version": "1.2.3"
}
```

An empty body or `{}` performs a settings lookup without changing metadata. Other update fields are rejected; a client cannot change its nickname, identity, permissions, or another device through this endpoint.

Response for both updates and lookups:

```json
{
  "ok": true,
  "device_id": 2,
  "server_at": "2026-04-10T08:00:00Z",
  "settings": {
    "file_max_bytes": 5242880,
    "text_batch_limit": 200
  }
}
```

A successful call refreshes the device's last-seen time. It is useful after an application upgrade, a WebSocket reconnect, or whenever the client wants current settings. It is optional; clients do not need to call it periodically to keep a connection open. Online status depends on an authenticated active WebSocket connection.

### `POST /api/v1/client/items/text`

Request:

```json
{
  "upload_kind": "realtime",
  "client_created_at": "2026-04-09T10:20:30Z",
  "text_content": "hello world"
}
```

`text_content` is required and must be a nonempty string. `upload_kind` accepts `realtime` or `history` and defaults to `realtime` when omitted. `client_created_at` is optional under the common timestamp rules.

Response: the compact upload receipt above. Both upload kinds participate in the recent-text window according to creation time. Realtime uploads may produce `clipboard.created`; history uploads never produce that event, but a changed window produces `clipboard.sync_changed`.

### `POST /api/v1/client/items/text/batch`

Request:

```json
{
  "items": [
    {
      "upload_kind": "history",
      "client_created_at": "2026-04-09T10:20:30Z",
      "text_content": "first"
    },
    {
      "upload_kind": "history",
      "client_created_at": "2026-04-09T10:21:00Z",
      "text_content": ""
    }
  ]
}
```

Response:

```json
{
  "results": [
    {
      "created": true,
      "item": {
        "item_id": 13,
        "content_kind": "text",
        "upload_kind": "history",
        "mime_type": "",
        "received_at": "2026-04-10T08:01:00Z"
      }
    },
    {
      "created": false,
      "error": "text_content must be a nonempty string"
    }
  ]
}
```

- `results` has exactly one entry per input item in the same order, including failures. Match results by array index.
- Each item follows the single-text validation and defaults; batch upload does not implicitly change `upload_kind` to `history`.
- Per-item validation permits partial success. A failed result has no `item` and creates no record.
- `items` must be a nonempty array. Malformed request structure or a count exceeding `text_batch_limit` rejects the whole request before any items are stored.
- History imports should explicitly use `upload_kind: "history"`. Successful realtime entries still follow realtime delivery rules.
- History-induced window changes are coalesced to at most one `clipboard.sync_changed` notification per recipient per batch request, containing the final committed visible window IDs. For a mixed batch, send this notification if at least one history entry succeeds and the final window ID list differs from the pre-request list; successful realtime entries also produce their usual events. Failed entries never enter the window.

### `POST /api/v1/client/items/file`

Upload one file using `multipart/form-data`.

| Form field | Required | Meaning |
| --- | --- | --- |
| `upload_kind` | No | `realtime` or `history`; defaults to `realtime` |
| `client_created_at` | No | RFC3339 creation time; defaults to receipt time |
| `mime_type` | No | MIME hint; the server detects the actual MIME type from bytes |
| `file` | Yes | Exactly one file |

Response:

```json
{
  "created": true,
  "item": {
    "item_id": 14,
    "content_kind": "file",
    "upload_kind": "realtime",
    "mime_type": "text/plain; charset=utf-8",
    "received_at": "2026-04-10T08:02:00Z"
  }
}
```

The server enforces the current `file_max_bytes` rules, including `0` and `-1`. Rejected uploads must not create clipboard records. The original filename is preserved when possible for delivered file metadata and download headers, but is not returned in the upload receipt. Files never enter the text synchronization window. History file uploads are stored without realtime delivery.

### `GET /api/v1/client/items/{item_id}/blob`

Download retained file bytes using the file's `item_id`, for example `/api/v1/client/items/14/blob`. The URL path shape is unchanged; its parameter refers directly to the clipboard row's database ID.

The response body is binary, with the file MIME type and `Content-Disposition: inline`; a filename is included in that header when available. Clients receiving file events can use `mime_type`, `byte_size`, and `blob_name` from the event as metadata. The server does not return a separate download URL. Missing/deleted/non-file items are unavailable even if the client still has old metadata.

### `POST /api/v1/client/sync/text`

Inspect the current synchronization window or fetch selected full text items. This endpoint is optional, requires a non-deleted device in the same account, and does not require a WebSocket connection. Device send/receive permissions are managed by the server and are not exposed as client settings. There is no global realtime fanout switch. A device without realtime receive permission always receives `{"item_ids":[],"items":[]}`, regardless of the requested IDs or `include_content`. Permissions are checked on every request; the response must not disclose even item IDs to a device without receive permission.

For a device allowed to receive, its own items in the window are readable; items from other devices are readable only while the source device allows sending. Items from a source marked deleted are omitted, although its row and historical associations remain stored. Apply this filter to the existing 50-item window, without fetching older database items to fill filtered positions. These rules apply equally to the ID list, requested contents, and synchronization notifications. They do not expose a separate permissions field or change administrator access to stored history.

The request has two required fields:

| Field | Type | Meaning |
| --- | --- | --- |
| `item_ids` | Array of positive integer IDs | Empty: select the whole current window. Nonempty: select matching items from that window only |
| `include_content` | Boolean | Whether to return the selected full text items |

For an authenticated device allowed to receive, the response always has two arrays, `item_ids` and `items`:

- For an empty request ID array, return the currently visible window IDs in `client_created_at DESC, item_id DESC` order, possibly fewer than 50 or empty.
- For requested IDs, return only those still present and readable in the window, in request order, removing duplicate IDs after their first occurrence. Unknown IDs, files, expired/deleted items, other-account items, items outside the window, and items hidden by source permissions are omitted without failing the request. This endpoint does not search the rest of database history.
- `include_content: false` always yields `items: []`.
- `include_content: true` yields one full text item per returned ID, in exactly the same order; both arrays describe the same server snapshot.

List the current window without content:

```json
{
  "item_ids": [],
  "include_content": false
}
```

Example response with only two retained texts:

```json
{
  "item_ids": [200, 100],
  "items": []
}
```

Fetch selected contents:

```json
{
  "item_ids": [100, 200],
  "include_content": true
}
```

Response, if both are still in the window:

```json
{
  "item_ids": [100, 200],
  "items": [
    {
      "item_id": 100,
      "source_device_id": 2,
      "content_kind": "text",
      "upload_kind": "history",
      "mime_type": "",
      "byte_size": 5,
      "char_count": 5,
      "client_created_at": "2026-04-09T10:20:30Z",
      "received_at": "2026-04-10T08:00:00Z",
      "text_content": "first"
    },
    {
      "item_id": 200,
      "source_device_id": 3,
      "content_kind": "text",
      "upload_kind": "realtime",
      "mime_type": "",
      "byte_size": 6,
      "char_count": 6,
      "client_created_at": "2026-04-09T10:21:00Z",
      "received_at": "2026-04-10T08:01:00Z",
      "text_content": "second"
    }
  ]
}
```

The other combinations are also valid: `item_ids: []` with `include_content: true` fetches the whole window and its contents; a nonempty array with `false` checks which requested IDs remain in the window. If nothing matches, return `{"item_ids":[],"items":[]}`.

## WebSocket Protocol

Connect to `/api/v1/client/ws`, deriving `ws://` from an HTTP server address or `wss://` from HTTPS. The only application message sent by the client is the initial `hello`. There are no application-level acknowledgement or keepalive messages. Standard WebSocket control frames remain transport details handled by the WebSocket implementation.

### Client Message: `hello`

The first message must be:

```json
{
  "type": "hello",
  "payload": {
    "device_token": "device-token",
    "device_uuid": "stable-install-uuid"
  }
}
```

Both fields are required and must identify the same non-deleted device. Invalid authentication closes the connection without a successful acknowledgement.

### Server Message: `hello_ack`

```json
{
  "type": "hello_ack",
  "payload": {
    "device_id": 2,
    "settings": {
      "file_max_bytes": 5242880,
      "text_batch_limit": 200
    },
    "time": "2026-04-10T08:03:00Z"
  }
}
```

The connection is ready for events only after `hello_ack`. Re-registering or deleting the device invalidates affected authenticated connections. Reconnect with the currently valid credential.

### Server Event: `clipboard.created`

Text event:

```json
{
  "type": "clipboard.created",
  "item": {
    "item_id": 15,
    "source_device_id": 3,
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

File event:

```json
{
  "type": "clipboard.created",
  "item": {
    "item_id": 16,
    "source_device_id": 3,
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

Download this file through `GET /api/v1/client/items/16/blob`. Other file types use the same schema, for example `mime_type: "application/pdf"`. Filename and size remain in delivered file metadata even though upload receipts omit them.

A realtime event is sent only when source sending is enabled, the source and target are not deleted and the target allows receiving, the target has an authenticated active connection, and it is a different device from the source. Only `upload_kind: "realtime"` generates this event. Storage and the text window still update if delivery is suppressed. Sending clients use their upload result to associate local data with the assigned `item_id`.

### Server Event: `clipboard.sync_changed`

```json
{
  "type": "clipboard.sync_changed",
  "payload": {
    "item_ids": [200, 100]
  }
}
```

This event carries the complete current text window ID list visible to its recipient after the same permission checks as the HTTP synchronization endpoint, ordered by `client_created_at DESC, item_id DESC`, with at most 50 IDs and no item content. An empty list means the recipient has no visible items in the window.

- A history text upload (single or batch) that changes the window list triggers this event. “Large update” has no numeric threshold: any history-induced membership/order change qualifies.
- A batch sends at most one such notification for its committed history-induced changes, using the final window list, even if many rows enter or leave it.
- Deletion, expiry, or clearing history also triggers it when the window list changes. Stored older text may enter the window to replace removed entries.
- Pure realtime uploads use `clipboard.created`, not this event. A history upload that leaves the window unchanged produces no notification. File uploads never change the text window.
- The event reaches eligible authenticated online devices that have not been deleted in the account, including the uploader if it has receive permission, with a separately filtered list for each recipient. Devices without receive permission receive neither this event nor `clipboard.created`, including empty synchronization notifications. Permissions are checked before delivery, not only during the initial hello.
- Changes to source sending permissions, source deletion, or restoration through registration that change a recipient's visible list also trigger a fresh filtered list. Enabling receive permission on an existing authenticated connection sends the newly visible list; disabling it stops delivery without sending a permissions message.
- It requests reconciliation; it is not a command to replace the system clipboard. It is not retained or replayed for offline clients.

## Recommended Client Flows

### Initial Connection And Reconnection

```mermaid
sequenceDiagram
    participant C as Client
    participant S as Server
    C->>S: WebSocket hello
    S-->>C: hello_ack with settings
    Note over C: Start buffering live events
    opt Refresh application metadata or settings
        C->>S: POST device/profile with metadata or empty body
        S-->>C: device_id and settings
    end
    C->>S: POST sync/text (item_ids=[], include_content=false)
    S-->>C: Current window IDs; items=[]
    Note over C: Compare with locally stored item IDs
    opt Missing IDs exist
        C->>S: POST sync/text (missing IDs, include_content=true)
        S-->>C: Still-present IDs and full text items
    end
    Note over C: Merge by item_id, process buffered events, reconcile if changed
    S-->>C: clipboard.created or clipboard.sync_changed
```

Connect before synchronizing to reduce the gap between the snapshot and live delivery. Merge content by `item_id`; an item can appear both in an HTTP response and a buffered event without representing two clipboard entries. A client may instead request all contents in one call when simplicity matters more than transfer size.

HTTP responses and WebSocket events have no shared sequence or version. The two synchronization requests may observe different snapshots: an ID can leave the window before its content is requested. Do not treat omitted IDs as permanent errors or blindly replace a newer local view with a delayed response/event. Serialize reconciliation work, buffer incoming events, and fetch a fresh full ID list if the window changed during a fetch or an expected item was omitted. Merge fetched content into local history separately from the server-window membership list. A fresh `item_ids: [], include_content: true` response supplies one internally consistent snapshot. Repeating reconciliation after concurrent activity settles makes the local window converge; the protocol does not promise a gap-free event log.

When a realtime text arrives, the client can merge it and sort/truncate its current window to 50. The source client can use its upload receipt plus submitted content, or refresh the window if it needs server-assigned creation time. Realtime items with older creation times may fall outside the window. Administrative permissions constrain both delivery and HTTP synchronization. An empty synchronization result does not distinguish an empty window from unavailable receive permission; it is not an instruction to erase local history. The client does not need to know whether other devices are online or permitted to send. Because filtered-out realtime uploads can still change the underlying 50-item window without delivering an event to this client, explicitly refresh through HTTP when exact current membership is needed.

### History Import And Window Replacement

```mermaid
sequenceDiagram
    participant A as Uploading client
    participant S as Server
    participant B as Other online client
    A->>S: POST items/text/batch (upload_kind=history)
    Note over S: Store valid entries; recompute recent text window
    S-->>A: Results in input order
    Note over A,B: Notifications require receive permission
    alt Window ID list changed
        S-->>A: clipboard.sync_changed with visible window IDs
        S-->>B: clipboard.sync_changed with visible window IDs
        Note over B: Compare IDs; keep local history independently
        B->>S: POST sync/text (missing IDs, include_content=true)
        S-->>B: Matching window items
    end
```

An imported item can have a high database ID and an old creation time, so it may not enter the window at all. Conversely, importing many recent texts can replace all 50 entries. Clients should compare sets of IDs and use the returned full ordering, never fetch “IDs greater than the last one.” History synchronization should update local history without automatically pasting every imported item or re-uploading received items.
