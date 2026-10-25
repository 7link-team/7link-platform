# Channel connector contract

Channel owners configure capabilities through `PUT /channels/{channel_id}/connectors/{kind}`. Kinds are `HOOK`, `HTTP_BRIDGE`, `TRANSCRIPTION`, and `SEARCH`. Capabilities are absent by default. Configuration uses version 0 to create and the returned version to update, plus an `Idempotency-Key`. Provide a signing secret of 32 or more characters on creation and endpoint changes. Secrets are write-only and encrypted using the deployment connector key ring.

The HTTPS provider endpoint must be public, must not include credentials, query parameters or a fragment, and must not redirect. Network address validation uses the platform HTTP policy at connection time. Providers verify `X-Channel-Signature: sha256=<hex HMAC-SHA256(secret, timestamp + "." + raw request body)>` and `X-Channel-Timestamp` (Unix seconds), reject stale timestamps, and deduplicate `Idempotency-Key`.

## Hooks and HTTP bridge

Outbound JSON contains `delivery_id`, `channel_id`, and `message` (current text message fields). Attachments are excluded. Only `CHANNEL` visibility messages are queued. Internal notes are never queued. Delivery checks the configuring Human is still a current channel owner, the channel is active, the configuration version is unchanged, and the message is not retracted. Network I/O occurs outside the database transaction. A successful 2xx response records DELIVERED. Other responses or transport errors retry with exponential delay, up to five attempts; then state is FAILED. Revoked or changed configurations cancel existing pending deliveries. The stable delivery identifier is the receiver's deduplication key; delivery is at least once.

`GET /channels/{channel_id}/connector-deliveries` exposes actual state/attempt count with offset paging (default 50, maximum 200), without message bodies, secrets or upstream error text.

`POST /channels/{channel_id}/bridge-messages` accepts `{ "body": "text" }` with an authenticated current Human channel owner and an idempotency key. It uses ordinary message authorization, audit and outbox. The originating HTTP_BRIDGE connector is excluded from outbound fanout. This does not impersonate an external chat user. Native IM, MQTT and WebSocket protocols are not implemented.

## Transcription provider

`POST /channels/{channel_id}/messages/{message_id}/attachments/{attachment_id}/transcription` sends `{ "mime": "audio/webm", "audio_base64": "..." }` to the configured TRANSCRIPTION endpoint. It reads only the pinned attachment through current message authorization; accepts audio up to 25 MiB; and accepts `{ "text": "actual transcription" }` of at most 8,000 characters. The UI allows Human review/editing and adding the result to a message draft. No transcription is generated locally or fabricated when the provider is unavailable.

## Search provider

`POST /channels/{channel_id}/search-cards` accepts `{ "query": "...", "approved": true }` and an `Idempotency-Key`. Each query requires current owner authority, a configured SEARCH capability, and explicit Human approval of disclosure to the provider, including any confidential content. The approval and request identity commit before the external request; query content is excluded from audit/outbox. Provider retries reuse that request identity. The provider receives `{ "query": "..." }` and returns:

```json
{
  "sources": [{ "title": "Title", "url": "https://example.com/source", "summary": "Extract" }],
  "conclusions": [{ "text": "Conclusion", "sources": [1] }]
}
```

Sources are limited to five HTTP(S) URLs. Conclusion references are one-based and must resolve; uncited conclusions are rejected. Provider response bodies are limited to 128 KiB; a persisted card must also fit the existing 32 KiB structured-message payload limit. Successful responses recheck current authorization and configuration, then atomically store an immutable `SEARCH_CARD` message with audit/outbox. The response includes its `message_id`. Cards remain INTERNAL even in client-shared channels. The receipt pins the approved connector identity and version. A failed attempt cannot redisclose through a changed or disabled connector. A completed retry returns the stored response without querying the provider again, including after connector rotation or disablement; current Human owner authority is still required. Missing or null conclusion arrays are normalized to an empty array. Failed persistence rolls back the message and permits retry with the same provider request identity. Provider failures do not synthesize content or claim a search occurred successfully.
