# Bastion · 家庭网络状态页

黑色 / 炭灰的只读状态页，独立展示控制面、数据链路、家庭出口和业务观测。未知与未测试不等于故障。

已实现 **Go + SQLite 后端、轻量采集器和真实 API 前端**。源码可运行不等于真实设备已全部接入；部署、业务探针和控制服务迁移应分别验收。

> 公开仓库只含通用代码与模板，不含实际拓扑、地址、域名、节点身份、凭据、数据库或日志。运行配置必须放在仓库外。尚未附带 LICENSE，不据此声明开源许可。

## 架构

```text
家庭采集器 --认证 HTTPS--> 边缘前端 / Caddy
                                 |
                           独立 WireGuard
                                 |
                        私网 Go API + SQLite
浏览器 --同域 HTTPS--> 前端 + 脱敏只读 API
```

前后端分机；API 不绑定公网，浏览器不访问设备管理接口。上报凭据按采集器独立分配，仅可写授权检测项。

## 能力与边界

- 总览、服务、连接路径、事件四页；黑色宽幅分区、16px 正文、至少 13px 辅助文字；关注优先预览、完整服务筛选与详情。
- 后端认证、去重、源心跳、当前状态、事件及 SQLite 持久化。
- 多采集器独立新鲜度；检测项独立有效期；API 失败显示未知，绝不恢复演示绿灯。
- 离线队列有数量/容量上限，补传只进入历史，不刷新当前状态或心跳。
- 历史从采集上线开始；缺口保留未知，图表断线；可用率与覆盖率分别统计。
- HTTP、DNS UDP/TCP、独立无额外提权 ICMPv6、Tailscale 本机状态、既有 peer-pinger 只读证据、隧道 ready/连接数、Linux 容量、固定命令/进程/任务/PVE/端口适配。
- **尚不包含**外部播放/下载验证、通知或控制服务迁移；基础网页或端口成功不等于实际业务通过。
- 采集认证不等于浏览器登录。公开读摘要或登录访问必须在边缘入口明确配置。

## 本地检查

需 Node 22+、Go 1.26.9+；Chrome 用于浏览器检查。Go 自动工具链仅影响本项目。

```bash
go test ./...
go vet ./...
go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...
cd frontend
npm ci
npm test
npm run check:ui
npm run check:live
npm run build
```

`check:ui`：79 项界面检查，临时启用明确的演示构建，结束后恢复生产构建。
`check:live`：本机真实 Go/SQLite 服务与 Chrome 的 48 项集成检查，输入为临时生成的测试夹具，**不是家庭数据**。不使用 MCP 或个人浏览器配置。

## 前端运行

```bash
cd frontend
npm run dev
```

默认从 `/api/v1/status` 获取真实摘要；无后端时显示未知。开发时可通过服务器环境变量 `BASTION_DEV_API` 指定本机后端，地址不会进入浏览器构建。

纯视觉演示另用 `npm run dev:demo`，打开 `http://127.0.0.1:5173/?demo=1`。**正常生产构建禁用演示，添加 URL 参数也不能启用。**

以下截图为新版布局的合成演示，不是实际部署状态：

![合成桌面总览](frontend/artifacts/overview-desktop.png)

## 文档

- [运行、部署及当前限制](docs/runtime.md)
- [版本化协议与状态语义](docs/protocol.md)
- [前端使用](frontend/README.md)
- [视觉约定](frontend/DESIGN.md)
- [通用设计（公开脱敏版）](home-network-status-project.md)
- [原始实施阶段计划](home-network-status-implementation-plan.md)

`deploy/` 为通用模板，不能原样用于生产；实际目标、秘密文件与访问策略必须单独配置。
