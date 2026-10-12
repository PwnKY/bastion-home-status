# Continuous checks and raw observations

Collectors already run continuously with per-check schedules. Most live probes run every 30 seconds; resource/web/job-record readers often run every 60 seconds. Frontend snapshot refresh is 10 seconds. These periods are not interchangeable, and any periodic check can miss a transient failure between measurements.

## Inspect a time window

Open **逐次观测记录** or the history link in an individual check's detail. Select a service and last 1h / 6h / 24h / 7d, or enter a custom start/end in **Beijing time**. The displayed timezone is fixed, independent of the browser timezone. Each page contains at most 1000 records; older-page navigation keeps the same fixed query window. Preset windows on an untruncated page refresh every 30 seconds; custom windows and paginated views stay fixed until requested again.

Rows show raw single-probe outcomes. A single failure remains visible even if it did not meet the backend's three-failure confirmation threshold. Confirmed current state still recovers after two successes. A raw failure count is not confirmed downtime, duration-weighted availability or an SLA. The 30-minute endpoint trend remains a separate, explicitly sampled view.

Evidence gaps are annotated only between located records present on the page, when prior evidence has expired before the next record. They indicate missing continuous valid evidence, not proven network downtime. A page may not cover the whole query period; counts and gap annotations apply only to that page. No missing period is backfilled green. Unlocatable replay reports null source time, is windowed by server receipt, and is placed after all located records; it never creates current freshness.

## Read boundary and budget

`GET /api/v1/observations?service=...&from=...&to=...&cursor=...` reads one configured service with a maximum seven-day window and stable bounded cursor. `GET /api/v1/history?resolution=raw&service=...` is the equivalent alias, permitting existing protected edge read matchers to forward raw reads without changing their authentication policy. The browser uses this alias. Read access remains private-listener / independent edge browser authentication; collector Bearer tokens do not become browser read credentials.

Queries use indexed window/cursor seeks, decode only selected payloads, and retain the existing four-second server budget. Raw records are not added to the status snapshot; opening overview does not load all services' raw history. Limits, errors and cancellation never fall back to stale success or demo content.

## Tailscale data-plane measurement

The optional `tailscale-tsmp` adapter sends one TSMP request to one privately configured literal peer, after read-only local status confirms Running and unique membership. The operator's configured literal is the explicit authorization to probe that one peer: the per-peer `Online` field is a control-plane marker, not data-plane proof, and does not gate the probe, so the uniquely matched target is probed even when the marker is `false`. Recommended deployment: interval 30s, CLI timeout 3s, process budget 4s; no infinite pings, burst retries, login/reconnect or elevated capabilities. Self/absent/duplicate/inability/cancellation are unknown. Only an exact matched TSMP pong supplies RTT, and a matched pong is healthy regardless of the marker. Only an exact vendor no-reply result while the marker was true is a raw failure; the same exact no-reply with a false marker stays unknown. Other local command/daemon errors are unknown.

Direction: collector/gateway → selected client. TSMP traverses the encrypted node data plane but bypasses the host OS/application stack. It does not verify subnet routing, ACL access to applications, playback, download or user-to-home business access. Direct/DERP path is not inferred: a TSMP result can be healthy while path classification remains unknown. The existing peer-pinger configuration and process are untouched. Continuous probes generate traffic and may keep a path active; they are not passive traffic observations.

When replacing an older passive check with this adapter, records before the activation remain the old mechanism's evidence and retain their original summaries/evidence. They must not be reinterpreted as periodic TSMP tests. Older `tsmp_offline` rows recorded by a previous build were no-send assessments based on the control marker alone, not TSMP ping failures, and must not be reinterpreted as probe timeouts. Server, selected collector and frontend upgrades require isolated validation, private backups, checkpoints and formal HTTPS acceptance. Time synchronization and backup tasks are out of this feature's scope.
