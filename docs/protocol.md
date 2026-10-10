# Bastion 协议 v1

本文仅为公开通用协议，不包含实际部署参数。

## 信任与公开边界

- 后端配置为每个采集器指定独立秘密文件和允许写入的服务 ID；Bearer 凭据至少 32 字符，建议随机 32 字节的 hex。
- 推荐在每个采集节点本机生成原始凭据，后端只配置其 SHA-256 hex 文件（`secretHashFile`）。后端也兼容 `secretFile`，二者只能选一个。原始凭据不需要跨节点复制，不进入浏览器、数据库观测、URL 或日志；摘要本身不能作为 Bearer 登录。
- 浏览器仅 GET 脱敏摘要。采集 POST 需应用认证，即使流经 WireGuard 也不免认证。
- 请求不允许额外 JSON 字段，最大 256 KiB，每批最多 128 项。每个已认证源每分钟最多 120 次写请求。
- 观测只允许枚举、数字和有限时间元数据，没有原始 URL、IP、响应体或异常消息字段。公共名称/范围来自后端受信配置；拒绝地址、域名、URL 和长 opaque 字串。
- 检测结果仍是受信采集器提供的声明，认证不证明探针诚实；凭据泄露应停用/更换该源。

## 接口

| 方法 | 路径 | 用途 |
| --- | --- | --- |
| POST | `/api/v1/heartbeat` | 新鲜源心跳，需 Bearer |
| POST | `/api/v1/ingest` | 当前/历史批次，需 Bearer |
| GET | `/api/v1/status` | 版本化摘要，无管理配置 |
| GET | `/api/v1/history?service=ipv4&range=24h` | 48 个半小时窗口点 |
| GET | `/api/v1/history?service=ipv4&range=7d` | 独立的 336 个窗口点 |
| GET | `/api/v1/incidents` | 最多 100 条异常/恢复事件 |
| GET | `/healthz` | 私网存储可达性；边缘不公开代理 |

没有管理 API、任意命令接口或浏览器登录接口。读摘要能否公开由 Caddy 的访问策略决定。

### 心跳

```json
{"id":"unique_random_request_id"}
```

源由凭据确定，不信任请求内提供的 collector ID。去重后的重复心跳不刷新接收时间。离线心跳不缓存或补传。

### 批次

```json
{
  "id":"unique_batch_id",
  "replay":false,
  "samples":[{
    "id":"unique_observation_id",
    "serviceId":"dns",
    "status":"healthy",
    "code":"ok",
    "ageSeconds":0.4,
    "capturedAt":"2026-01-01T00:00:00Z",
    "latencyMs":12.5,
    "pathMode":"unknown"
  }]
}
```

- ID 为有界字母、数字、横线/下划线，推荐随机值；由服务端按源 + ID 去重。
- `status`：`healthy / degraded / down / unknown / untested / maintenance`。
- `code` 为固定枚举，对应通用摘要；不允许传入任意错误字符串。
- `latencyMs` 可为 null。`readyConnections`、`usagePercent` 为可选有界数值。
- `pathMode`：`unknown / direct / relay / mixed`，不能根据一般健康状态猜测。配置中的 HTTP `network=tcp4/tcp6` 限定地址族，`connectIp` 可绕过系统解析而保留原 TLS 主机名；不能与代理/Unix socket 混用。
- 实时年龄必须为非负有限数字；后端用接收时间减年龄，不使用 `capturedAt` 判定新鲜度。
- 采集器同一 Linux kernel boot 内利用 uptime 重建缓存年龄，容忍墙钟偏差；重启后不能可靠推算年龄的补传使用 null，仅保留私有证据，不绘入时间线。
- 所有缓存补传明确 `replay=true`，只进历史/证据，不能更新 current 或心跳；不论它声称时间多新。
- 较旧实时样本不能覆盖最新状态；同一毫秒的重复时序保守忽略 current 更新。

## 当前与历史语义

1. 服务必须同时拥有新鲜源心跳与未过期观测；每个源和检测项有独立有效期。恰好达到阈值即过期。
2. 没有数据、API 不可达、心跳缺失均为未知，不推断全部家庭出口故障。
3. 连续 3 次失败才确认 down；确认前为 unknown，不假装仍可用。已确认 down 需要连续 2 次成功恢复。
4. 容量预警/高风险均为 degraded，不宣称播放或下载已经失败。缺失挂载可以形成单独的 scoped 探测故障。
5. `untested` 不因过期改写成已测结论。本机 Tailscale Running 不证明控制同步；隧道 ready 不证明回源；Jellyfin health 不证明播放。
6. 历史为半小时窗口端点的抽样，不是全时段 SLA。上线前无记录的点保持未知。实时历史结合源心跳，不能让长 TTL 探针涂绿失联期间；可定位的补传补历史，不改当前。
7. available 分母只包含 healthy/degraded/down；未知、未测、维护排除；覆盖率单独给出。少量有效点得到 100% 不等于覆盖整个窗口。
8. 状态摘要带 `version=1`、`source=live`、`synthetic=false`、`serverTime`、`collectors[]`、服务、事件。兼容 `collector` 为第一个家庭源，仅供旧概览卡使用。
9. API 不缓存到浏览器；后端有最多 2 秒的共享计算缓存，并在认证写入后失效。前端在轮询间隔内继续以服务器基准 + 单调计时撤销过期结果。

## 保留

原始受限观测、请求去重、已解决事件默认保留 30 天；每小时执行清理。小时聚合保留 365 天，只表示原始探针结果计数，不冒充时间加权可用率。公开接口目前仅查询 24h/7d 原始窗口。

SQLite 使用 WAL、单连接、事务、参数化查询；current 与历史不会因进程重启丢失。未解决事件保留；长期运行仍需监测磁盘、数据库增长和队列丢弃日志。
