# Read-only local adapters

The collector additionally supports these private-config check kinds:

| Kind | Fields | What it proves |
| --- | --- | --- |
| `unit` | `unit` | Loaded systemd unit active; not application success |
| `job` | `unit`, `maxAgeSeconds` | Latest oneshot succeeded within the age budget; inactive is normal |
| `pve` | `resourceType`: node/qemu/lxc, `resourceId` | One matching local PVE resource online/running; not guest routing/business |
| `tcp` | `target`, optional `expectedText` prefix | Port accepts a connection and optional banner; not authenticated business |
| `peer-poll` | `path` | Existing peer-pinger local status poll fresh within 30 seconds; not control-server synchronization |
| `peer-pinger` | `path`, `maxAgeSeconds` | Fresh ping events from currently online/present peers; not all peers or application traffic |

No adapter starts/stops a unit or invokes a shell. systemctl show and pvesh get use fixed argument lists, six-second deadlines and one-MiB output budgets. Unit names are validated. Stopped templates should not be configured as expected-running resources. Jobs use monotonic execution timestamps; missing/reboot-invalid timestamps are unknown, never assumed success.

The peer adapter never runs a ping. It reads version-1 evidence, excludes stale/future/offline peers and publishes no peer names, addresses or raw output. A fresh failed subset is degraded rather than a claim that every link is down. It preserves evidence age and a stable source-event ID so rereading a file cannot refresh old evidence or multiply failure counts. Missing event evidence remains unknown. Its default age budget is 180 seconds; configure it no longer than the backend service validity window. The existing event-triggered pinger may legitimately produce no fresh event during a stable connection; do not add a ping loop merely to make the dashboard green.

HTTP checks accept an optional per-check `caFile`. Trust is added explicitly; certificate hostname verification is never disabled. This permits private CA management interfaces without publishing their names or certificates in example configs.

Cloudflared metrics are read even when readiness fails, so a measured zero connection count is retained. It is not a synthetic default.

Upgrade the backend first, because new adapters use additional fixed summary codes. Existing credentials and queues need not be replaced. Deployment configuration, host-only DNS mappings, credentials, evidence and logs remain outside public source control.
