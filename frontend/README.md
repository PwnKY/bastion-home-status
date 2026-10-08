# 家庭网关状态页前端（BLACK 深色视觉原型）

本目录是「家庭网关状态页」的**已完成前端视觉原型**：黑色/炭灰深色主题，克制的鼠尾草绿与琥珀色强调色，
四个可路由页面、筛选、服务详情弹窗、24h 合成历史与状态语义判定均已在纯前端实现，可直接 `npm run dev` 浏览。

该原型仍然**只有合成演示数据**：没有后端、没有认证、没有真实探针、没有设备连接，也没有使用 MCP。
UI 不调用任何真实设备接口或后端 API，也不加载外部字体或 CDN 资源；全部字体均为本地系统字体栈，
图标为内联 SVG，收藏图标 `public/favicon.svg` 为本地文件。开发服务器在浏览器加载时仍会通过本地 HTTP
取回页面自身的 HTML/CSS/JS 资源（这是 dev server 的常规行为），但这不涉及任何真实设备或后端 API 请求。

> 重要声明：不连接任何设备、SSH、RouterOS、Headscale、DERP 或公网采集器；**不发起任何真实设备或后端 API 请求**。
> 全部数据均为 `source=demo` 的**合成演示数据**，不含真实 IP、节点名、域名、日志或任何凭据字段，
> 不代表任何真实设备、网络、链路或业务状态。

## 快速开始

```bash
# 按锁文件安装依赖（离线环境见下方说明）
npm ci

# 本地开发服务器：http://localhost:5173/（脚本绑定 127.0.0.1）
npm run dev

# 运行单元测试（node --test，无需网络）
npm test

# 生产构建
npm run build

# 预览构建产物
npm run preview

# 本地 Chrome 浏览器检查与截图（需要 Node 22+ 和已安装的 Chrome，不使用 MCP）
npm run check:ui
```

环境要求：本机实际验证版本为 **Node.js v22.23.2 / npm 11.16.0**，安装并构建所用的 Vite 实际版本为 **6.4.3**
（`package.json` 声明 `^6.4.3`，`package-lock.json` 与 `node_modules` 均为 6.4.3）。
Vite 6 的引擎要求为 `^18.0.0 || ^20.0.0 || >=22.0.0`，因此当前 Node 22.x 可直接使用。

### 离线安装说明

若当前环境无法访问 npm registry，而本机 npm 缓存中已有对应依赖，可使用：

```bash
npm ci --offline
```

本目录的 `package-lock.json` 中 `resolved` 指向 `registry.npmmirror.com` 镜像。已验证本机缓存可以完成离线安装；换到其他机器时不保证缓存存在。缓存缺失时需要联网运行 `npm ci`，更改 registry 不能凭空补齐离线缓存。

## 界面与交互

四个哈希路由（无后端、无路由库，仅 `window.location.hash`）：

| 路由 | 页面 | 内容 |
| --- | --- | --- |
| `#overview` | 网络总览 | 概览统计卡、网络/接入分组服务列表、回家链路面板、出口延迟趋势、最近事件 |
| `#services` | 服务观测 | 全部分组服务表、关键字搜索、状态筛选、分组标签页 |
| `#paths` | 连接路径 | 合成拓扑、控制/数据/隧道观测、出口路径对照、三个边界问题 |
| `#events` | 事件时间线 | 事件状态筛选（全部/观察中/已恢复/记录）与事件卡片 |

其他已实现的交互：

- **顶部场景选择器**：`正常 / 降级 / 采集器失联` 三种合成场景，切换即重新生成合成快照。
- **刷新按钮（刷新演示）**：只重新生成新的**合成**快照，不会请求真实设备。
- **服务详情弹窗**：点击任意服务/路径卡片打开 `#service-dialog`（原生 `<dialog>.showModal()`），
  展示观测来源、最近观测时间、24h 可用率、覆盖率、历史上报值与检测边界说明。
- **24 小时 / 7 天切换**：24h 显示 48 个等间隔合成样本趋势；**7 天暂无数据，界面显式显示空态**
  （“还没有 7 天观测数据”），不会用 24h 数据冒充 7 天历史。
- **过期语义**：服务/采集器心跳超过 **90 秒**即视为过期，当前状态置为“未知”，
  当前延迟置空（历史上报值仍在弹窗中审计性展示）。刷新与场景切换重新生成的仍是演示数据。

## 目录结构

```
frontend/
├─ index.html          # 入口：跳过链接、#app、aria-live toast、#service-dialog
├─ package.json        # 脚本与 devDependency（vite ^6.4.3）
├─ package-lock.json   # 锁定版本（vite 6.4.3、esbuild 0.25.12 等）
├─ public/favicon.svg  # 本地 SVG 图标（无外部资源）
├─ src/
│  ├─ data.js          # 合成演示数据层（纯函数，确定性）
│  ├─ model.js         # 纯函数模型层：判定语义、统计、格式化
│  ├─ main.js          # 渲染与交互层（shell/页面/筛选/弹窗/路由）
│  └─ styles.css       # 深色视觉样式（CSS layers，含响应式与减少动效）
├─ tests/
│  ├─ data.test.js     # 数据契约与隐私测试
│  └─ model.test.js    # 派生规则、边界、统计与格式化测试
├─ scripts/browser-check.mjs # 本机 Chrome/CDP 检查，隔离临时浏览器配置，运行后关闭
├─ artifacts/          # 浏览器检查生成的桌面、手机和详情截图
├─ DESIGN.md           # 视觉、响应式、可访问性与未来 API 边界说明
└─ dist/               # 构建产物（已 gitignore）
```

## 本地浏览器验收

`npm run check:ui` 使用本机 Chrome 的 DevTools 协议，不使用 MCP，不添加自动化依赖，不读取你的浏览器配置或登录会话。脚本启动临时的本地预览服务器和独立无头浏览器，完成后关闭，并清理临时配置。

- 覆盖四个页面、搜索、筛选、场景、弹窗、Escape、手机导航和过期结果。
- 检查 1440 / 1024 / 768 / 390 / 320px 下四个页面没有横向溢出。
- 检查浏览器运行错误和非本地页面资源请求。
- 截图写入 `artifacts/overview-desktop.png`、`overview-mobile.png`、`paths-desktop.png`、`service-detail.png`。
- 未自动找到 Chrome 时设置 `CHROME_PATH`。这项检查使用 Node 22+ 的全局 WebSocket；普通构建环境要求仍按 Vite 引擎约束。

## 数据契约

### 快照（`createDemoSnapshot(scenario, now)` 返回）

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `sampledAt` | ISO string | 采样时间 |
| `source` | `'demo'` | 数据来源标记 |
| `synthetic` | `true` | 合成数据标记 |
| `disclaimer` | string | 合成免责声明 |
| `scenario` | `'healthy' \| 'degraded' \| 'lost'` | 场景 |
| `collector` | object | `{ lastHeartbeatAt, intervalSeconds, staleAfterSeconds }` |
| `services` | array | 服务条目（见下） |
| `metrics` | object | `{ latencyTrend, coverageTrend }`，均为 `{ at, value }[]` |
| `incidents` | array | 演示事故时间线 |

### 服务条目字段

`id`、`name`、`subtitle`、`group`（`network`/`access`/`application`/`monitoring`）、
`status`（上报状态）、`latencyMs`、`availability24h`、`availability7d`、`coverage`、
`observedAt`、`probeLabel`、`scope`、`summary`、`history`、`detail`。

- 状态取值：`healthy`、`degraded`、`unknown`、`untested`、`maintenance`、`down`。
- `history`：24 小时、每 30 分钟一个样本，共 48 条；最后一条样本与“最新状态”一致。
- `availability7d`：当前演示只有 24 小时历史，**显式为 `null`**，不会把 24h 值复制成 7d 分母。

### 派生视图（`deriveView(snapshot, now)` 追加字段）

- 每个服务新增：`reportedStatus`、`reportedLatencyMs`、`stale`、`observedAgeMs`、`observedAgeSeconds`；
  过期时 `latencyMs` 置为 `null`（当前值），原始值保留在 `reportedLatencyMs`。
- `collector` 新增：`heartbeatAgeMs`、`heartbeatAgeSeconds`、`stale`。
- 顶层新增：`statusCounts`、`overall`、`generatedAt`、`derivedAt`。

## 判定语义（核心约定）

1. **过期即未知**：服务 `observedAt` 距 `now` 的年龄 `>= collector.staleAfterSeconds`（默认 90 秒）时，
   `stale=true` 且当前 `status` 置为 `unknown`；`reportedStatus` 保留原始上报值。
   恰好 90 秒即算过期（`>=` 边界）。
2. **时间戳缺失/非法视为过期**：`observedAt` 为 `null`、`undefined` 或无法解析时按过期处理，
   绝不当作“刚观测过”。采集器心跳 `lastHeartbeatAt` 同理。
3. **未测试不因时间戳变旧而改写**：`untested` 始终保持 `untested`，不会因 `observedAt` 变旧而变成 `unknown`。
4. **历史延迟不是当前延迟**：过期服务的当前 `latencyMs` 为 `null`，避免 UI 把历史延迟当作当前延迟。
5. **整体状态顺序**：先判 `down`；若**没有任何已评估服务**则整体为 `unknown`（先于 degraded 分支）；
   否则存在 `degraded`/`unknown` 时整体为 `degraded`；再否则为 `healthy`。
6. **可用率分母**：`healthy + degraded + down` 计入分母；`unknown`、`untested`、`maintenance` 明确排除，
   不会把“未知”算成“失败”。`coverage` = 已观测样本 /（总样本 − 维护样本）。
7. **lost 场景不伪造故障**：家侧/采集侧观测过期记为 `unknown` 缺口（历史最近样本同样记为 `unknown`，而非 `down`）；
   独立公网采集器（`headscale-base`、`derp-base`）保持新鲜状态。
8. **合成历史包含少量历史故障**，因此可用率不会永远显示 100%。
9. 演示环境始终不宣称“所有业务已验证可用”（`overall.claimsAllBusinessTested=false`）。

## 导出 API 速览

`src/data.js`

- `SCENARIOS`：场景元数据 `[{ id, label }]`。
- `DEFAULT_SCENARIO`：默认场景 `'degraded'`。
- `createDemoSnapshot(scenario?, now?)`：生成确定性合成快照（纯函数）。

`src/model.js`

- `EXCLUDED_FROM_UPTIME`：`['unknown', 'untested', 'maintenance']`。
- `getHistoryStats(history)`：统计可用率、降级占比、覆盖率；空/非法输入安全返回 `null` 值。
- `deriveView(snapshot, now?)`：深拷贝派生展示视图，不修改输入。
- `formatLatency(ms)`：安全格式化延迟，空值/非法值返回 `—`，`<1000ms` 显示毫秒，否则显示秒。

## 明确不做的事（范围边界）

- 无后端、无 API 调用、无数据上报。
- 无认证、无登录、无权限控制。
- 无真实探针、无设备连接、无 SSH、无 RouterOS/Headscale 调用。
- 无 MCP 集成；不读取环境变量；运行时不发起真实设备或后端 API 请求。
- 不加载外部字体、CDN 或第三方脚本；测试与构建所需的 Vite 仅为 `devDependencies`。
- `availability7d` 在补齐 7 天真实历史前保持 `null`，UI 对 7 天范围显示空态。
