# Read-only local adapters

The collector additionally supports these private-config check kinds:

| Kind | Fields | What it proves |
| --- | --- | --- |
| `unit` | `unit` | Loaded systemd unit active; not application success |
| `job` | `unit`, `maxAgeSeconds` | Latest oneshot succeeded within the age budget; inactive is normal |
| `pve` | `resourceType`: node/qemu/lxc, `resourceId` | One matching local PVE resource online/running; not guest routing/business |
| `tcp` | `target`, optional `expectedText` prefix | Port accepts a connection and optional banner; not authenticated business |
| `icmp6` | `target`: explicit public IPv6 literal | One matched ICMPv6 Echo reply and monotonic RTT; not DNS, HTTPS, all routes or business |
| `peer-poll` | `path` | Existing peer-pinger local status poll fresh within 30 seconds; not control-server synchronization |
| `peer-pinger` | `path`, `maxAgeSeconds` | Fresh ping events from currently online/present peers; not all peers or application traffic |

No adapter starts/stops a unit or invokes a shell. systemctl show and pvesh get use fixed argument lists, six-second deadlines and one-MiB output budgets. Unit names are validated. Stopped templates should not be configured as expected-running resources. Jobs use monotonic execution timestamps; missing/reboot-invalid timestamps are unknown, never assumed success.

The peer adapter never runs a ping. It reads version-1 evidence, excludes stale/future/offline peers and publishes no peer names, addresses or raw output. A fresh failed subset is degraded rather than a claim that every link is down. It preserves evidence age and a stable source-event ID so rereading a file cannot refresh old evidence or multiply failure counts. Missing event evidence remains unknown. Its default age budget is 180 seconds; configure it no longer than the backend service validity window. The existing event-triggered pinger may legitimately produce no fresh event during a stable connection; do not add a ping loop merely to make the dashboard green.

HTTP checks accept an optional per-check `caFile`. Trust is added explicitly; certificate hostname verification is never disabled. This permits private CA management interfaces without publishing their names or certificates in example configs.

Cloudflared metrics are read even when readiness fails, so a measured zero connection count is retained. It is not a synthetic default.

## Bounded public IPv6 Echo

`icmp6` sends one 24-byte nonce payload per scheduled check, not a ping subprocess or an additional background loop. The receive deadline is at most three seconds (earlier parent deadlines/cancellation also apply), the receive processing budget is 16 packets, and success requires the configured source, Echo Reply type/code, kernel socket identifier, sequence and complete nonce. RTT starts immediately before sending, and is absent on failure; the usual backend confirmation/freshness rules apply.

Only unzoned public-unicast IPv6 literals are accepted. Hostnames, ports, IPv4/mapped addresses, multicast, loopback, link-local and private/ULA (including Tailscale IPv6 peers) are rejected. Linux uses a non-raw `udp6` ICMP ping socket. There is no raw-socket fallback, shell, resolver, proxy or extra capability. A missing/denied local socket is **unknown**, not target downtime; unsupported platforms do not fabricate measurements. Existing `peer-pinger` evidence remains read-only and unchanged.

Private configuration example (documentation address must be replaced with the intended public target):

```json
{"serviceId":"ipv6-icmp","kind":"icmp6","target":"2001:db8::10","intervalSeconds":30}
```

Use a new service/history ID instead of changing a DNS ID into an ICMP ID. The frontend selects a configured `ipv6-icmp` as the IPv6 baseline card even if it fails, without falling back to a healthy DNS result; retained DNS and HTTPS observations stay independent.

Preflight the exact deployed service security context with `bastion-agent -probe-icmp6 <public-IPv6-literal>`. This sends one Echo and emits only the sanitized observation; it reads no credentials/config, writes no queue and never reports to the backend. Exit status is nonzero unless the Echo succeeded. Do not enable a probe by granting privileges, adjusting `ping_group_range`, or relaxing unit/network policy just to obtain a green result.

Upgrade the backend first, because new adapters use additional fixed summary codes. Existing credentials and queues need not be replaced. Deployment configuration, host-only DNS mappings, credentials, evidence and logs remain outside public source control.
