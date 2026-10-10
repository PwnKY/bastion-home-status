# Read-only local adapters

The collector additionally supports these private-config check kinds:

| Kind | Fields | What it proves |
| --- | --- | --- |
| `unit` | `unit` | Loaded systemd unit active; not application success |
| `job` | `unit`, `maxAgeSeconds`, optional `journalHistory` | Latest oneshot succeeded within a verified monotonic age budget; inactive is normal |
| `pve` | `resourceType`: node/qemu/lxc, `resourceId` | One matching local PVE resource online/running; not guest routing/business |
| `tcp` | `target`, optional `expectedText` prefix | Port accepts a connection and optional banner; not authenticated business |
| `icmp4` | `target`: explicit RFC1918 IPv4 literal | One matched LAN ICMPv4 Echo reply and monotonic RTT; not Wi-Fi quality, internet or all clients |
| `icmp6` | `target`: explicit public IPv6 literal | One matched ICMPv6 Echo reply and monotonic RTT; not DNS, HTTPS, all routes or business |
| `peer-poll` | `path` | Existing peer-pinger local status poll fresh within 30 seconds; not control-server synchronization |
| `peer-pinger` | `path`, `maxAgeSeconds` | Fresh ping events from currently online/present peers; not all peers or application traffic |
| `control-stream` | `unit`, `maxAgeSeconds` (20–180), usually `intervalSeconds:30` | Recent growth of existing Tailscale control-response messages; not complete route/ACL synchronization |

No adapter starts/stops a unit or invokes a shell. systemctl show and pvesh get use fixed argument lists, six-second deadlines and one-MiB output budgets. Unit names are validated. Stopped templates should not be configured as expected-running resources. Jobs use monotonic execution timestamps; missing/reboot-invalid timestamps are unknown, never assumed success.

The peer adapter never runs a ping. It reads version-1 evidence, excludes stale/future/offline peers and publishes no peer names, addresses or raw output. A fresh failed subset is degraded rather than a claim that every link is down. It preserves evidence age and a stable source-event ID so rereading a file cannot refresh old evidence or multiply failure counts. Missing event evidence remains unknown. An expired event from an online/present peer can remain an optional, source-clock `evidence` reference without current path, RTT or success. Future records and offline/absent peers are still excluded. Its default age budget is 180 seconds; configure it no longer than the backend service validity window. The existing event-triggered pinger may legitimately produce no fresh event during a stable connection; do not add a ping loop merely to make the dashboard green.

HTTP checks accept an optional per-check `caFile`. Trust is added explicitly; certificate hostname verification is never disabled. This permits private CA management interfaces without publishing their names or certificates in example configs.

Cloudflared metrics are read even when readiness fails, so a measured zero connection count is retained. It is not a synthetic default.

## Optional systemd journal references

`journalHistory:true` is opt-in and valid only for `job`. The fallback judges a loaded oneshot only when it has no valid execution timestamp; otherwise it may attach the exact journal reference without changing the existing current-unit judgement. It never starts the job or changes the update script/timer. `journalctl` reads at most 24 filtered completion/failure records, within the command deadline/output budget. Records must match the configured unit, trusted PID 1/root journal metadata, fixed systemd message IDs and structured results; application text and default `Result=success` are not completion evidence.

A current-boot record can prove completion age using the kernel boot ID and journal monotonic timestamp. A previous-boot record **cannot prove elapsed age without trusted cross-host wall-clock validation**. This implementation therefore keeps it unknown and exports only fixed success/failure kind, unchanged source time and `historical:true`. It does not declare a previous-boot success fresh, overdue or recovered. Repeated reads are unknown monitoring attempts, not new task completions; the historical timestamp never renews. No raw journal text, unit identity or boot identifier enters the public evidence object.

## Passive recent control communication

The adapter invokes only fixed `systemctl show`, `tailscale status --json` and `tailscale debug metrics` reads within a shared six-second deadline and one-MiB per-command output limit. It verifies the daemon start identity before and after reading and requires Running/Online, an active control poll and a strictly increasing `controlclient_map_response_message` counter. Nonzero cumulative counters, a healthy process or Self.Online alone are insufficient. Metrics with missing/duplicate/labeled/nonfinite counters are rejected; raw metrics and node identities are discarded.

The first reading, agent/daemon restart, counter reset, backwards uptime, unreadable snapshot or excessive read gap resets the volatile baseline to unknown. No tracker persistence turns startup counters into new traffic. The oldest possible message time is the **start of the preceding read**; current wall-clock polling is not a message timestamp. Unchanged counters retain the same event ID/age, then become unknown at the configured age budget. A local health warning is degraded, not a claim that all peers are down. Configure the adapter age no longer than the backend service validity window. No login/up/down/reconnect, peer ping or daemon/network configuration change is performed.

This adapter requires the updated backend before collector activation: `checkedAgeSeconds` orders the newer assessment while `ageSeconds` still bounds evidence expiry. Queued/replayed observations age both clocks; replay never updates current. Reference timestamps shown by the frontend are explicitly source-clock records, not server-verified wall time.

## Bounded LAN IPv4 and public IPv6 Echo

`icmp4` and `icmp6` send one 24-byte nonce payload per scheduled check, not a ping subprocess or an additional background loop. The receive deadline is at most three seconds (earlier parent deadlines/cancellation also apply), the receive processing budget is 16 packets, and success requires the configured source, Echo Reply type/code, kernel socket identifier, sequence and complete nonce. RTT starts immediately before sending, and is absent on failure; the usual backend confirmation/freshness rules apply.

`icmp4` accepts only explicit RFC1918 IPv4 literals for LAN devices. Public IPv4, CGNAT (including Tailscale IPv4 peers), loopback, link-local, multicast, hostnames, ports and mapped IPv6 are rejected. It uses a non-raw Linux `udp4` ping socket. It does not measure Wi-Fi signal/airtime, wireless client connectivity or the internet. Add a distinct service/history ID such as `lan-router-ping`; keep the target only in private deployment configuration.

`icmp6` accepts only unzoned public-unicast IPv6 literals. Hostnames, ports, IPv4/mapped addresses, multicast, loopback, link-local and private/ULA (including Tailscale IPv6 peers) are rejected. Linux uses a non-raw `udp6` ICMP ping socket. There is no raw-socket fallback, shell, resolver, proxy or extra capability. A missing/denied local socket is **unknown**, not target downtime; unsupported platforms do not fabricate measurements. Existing `peer-pinger` evidence remains read-only and unchanged.

Private configuration example (documentation address must be replaced with the intended public target):

```json
{"serviceId":"ipv6-icmp","kind":"icmp6","target":"2001:db8::10","intervalSeconds":30}
```

Use a new service/history ID instead of changing a DNS ID into an ICMP ID. The frontend selects a configured `ipv6-icmp` as the IPv6 baseline card even if it fails, without falling back to a healthy DNS result; retained DNS and HTTPS observations stay independent.

Preflight the exact deployed service security context with `bastion-agent -probe-icmp4 <RFC1918-IPv4-literal>` or `bastion-agent -probe-icmp6 <public-IPv6-literal>`. The two flags are mutually exclusive. This sends one Echo and emits only the sanitized observation; it reads no credentials/config, writes no queue and never reports to the backend. Exit status is nonzero unless the Echo succeeded. Do not enable a probe by granting privileges, adjusting `ping_group_range`, or relaxing unit/network policy just to obtain a green result.

The ICMP adapters require backend support for `icmp_error` and `icmp_unavailable` (already present in v0.4.0-preview and later); IPv4 reuses these fixed codes and needs no backend binary upgrade on an already compatible deployment. Validate the complete updated definition configuration in isolation before replacing live configuration. Existing credentials and queues need not be replaced. Deployment configuration, host-only DNS mappings, credentials, evidence and logs remain outside public source control.
