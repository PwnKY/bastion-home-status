# 运行与部署

公开模板不含真实部署参数；复制到仓库外再填写。数据库、秘密文件、CA、缓存与日志不得提交。

## 固定拓扑

边缘节点承载静态前端/Caddy 与 HTTPS 上报入口，数据节点承载 Go/SQLite。两者通过独立点对点 WireGuard 通信。家庭主动向边缘上报，浏览器只访问同域摘要。

- 数据节点 API 绑定自己的 WireGuard 地址，**不能绑定公网或未指定地址**；本地开发可绑定回环。
- WireGuard 内层仅允许边缘 peer → 数据节点指定 TCP 端口，保留对端 /32 与应用认证。
- 原本仅放行 ICMP 的防火墙 helper 需要协调更新拥有的链，不能覆盖脚本后直接重复 up；必须保留无关规则、失败回滚与回归测试。
- 不配置默认路由、NAT、FORWARD 或家庭出口。不要把入口连通性当作被监控控制系统的健康。

## 构建与检查

```bash
go test ./...
go vet ./...
go test -race ./...  # 推荐 Linux；Windows 本机运行库不兼容时不能声称通过
go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o build/bastion-server ./cmd/bastion-server
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o build/bastion-agent ./cmd/bastion-agent
cd frontend
npm ci
npm test
npm run check:ui
npm run check:live
npm run build
```

Go 最低 1.26.9，锁文件固定 SQLite 依赖。正常生产前端构建禁用演示，不部署 `build:demo` 的产物。

## 后端

- 新建独立 `bastion-server` 系统用户；安装二进制至 `/usr/local/bin/bastion-server`。
- 私有配置 `/etc/bastion-server/config.private.json`，目录 root:服务组 0750，配置/各源秘密文件 0640 或仅服务账户可读。
- 每个采集节点本机生成不同的随机秘密，建议 32 字节 hex；后端只接收其 SHA-256 摘要文件，不搬运或打印原始凭据。摘要不具备 Bearer 认证能力。
- 数据目录 `/var/lib/bastion-server` 为服务账户所有、0700；SQLite 文件 0600。
- `deploy/server.example.json` 默认 loopback，只是模板。生产填写实际隧道绑定地址、秘密路径、公共角色标签及服务所有者。
- `deploy/bastion-server.service` 限制写入路径、内存、CPU 和权限。应用目录只新增，不覆盖其他业务。
- 启动前验证配置；启动后验私网 health、匿名读、无凭据写入拒绝，以及服务重启后的持久性。

## 采集器

- 独立凭据、配置和缓存目录；可一个家庭源汇总可达的内网接口，本机指标由小源提供。
- `deploy/collector.example.json` 仅示范本机检查。实际目标不写进源码，不经 API 动态传入。
- 默认 30 秒，单轮最多 4 个并行检查；心跳独立运行，避免一个慢探针拖成全源失联。
- 队列默认最多 2048 批 / 16 MiB，超限删除最旧批并记录通用警告，不无限吃满磁盘。
- HTTPS 验证 CA 和主机名；不忽略证书错误、不跟随重定向传凭据。私网 HTTP 仅在显式 WireGuard 模式下使用，启动和每次上报均验证路由经过项目接口。
- Tailscale LocalAPI socket 通常需要 root；只给确需本机读取的源此权限。其他源改用专用普通账户。模板不授予网络配置能力或任意远程执行权限。
- 禁止启动第二套 peer 自动 ping；现有 peer-pinger 适配尚待实现。未接入项目使用 untested，而不是随便根据 Running 填绿。
- 挂载容量只采集统计信息，不写文件、不测试播放/转码或下载。

## 边缘与访问策略

`deploy/Caddyfile.example` 默认只在回环预览；通过私有环境文件配置站点地址、绑定接口及后端 upstream。

- 先验收 loopback 预览，再配正式域名 DNS、TLS 和云端 80/443 规则；确认稳定公网地址。
- 是否公开摘要或要求登录必须明确。默认回环预览不是匿名公网开放许可。
- 正式 HTTPS 入口分离认证 POST 与只读 GET，只代理明确 API 路径；不公开 backend health/admin 或设备接口。
- 需要登录时在 Caddy 只读/静态路由加入独立访问控制，**不要**把家庭上报凭据当浏览器密码。
- 不启用包含凭据/请求体的访问日志。使用模板的 no-store 和安全响应头；静态 JS/CSS 只来自本站。

## 部署顺序与回滚

1. 私网后端 + systemd，确认原业务未受影响。
2. 协调更新单一隧道的 API 端口规则，验证仅正确 peer 可以访问。
3. 边缘只读预览与边缘源，确认 API 不通时前端不会假绿。
4. 正式域名/TLS/访问策略落实后，再启动家庭上报和本机小源。
5. 仅对新采集器做暂停/模拟输入验收；不得停止真实 DNS、代理、路由或隧道演练。

失败时停止新单元、恢复自己的配置/链，保留数据库和诊断证据；不要刷新全机防火墙、重启其他业务、调整 PVE 时间或备份策略。旧控制服务的迁移与升级保持为另一批任务，避免数据库同时写入分叉。

## 当前限制

原型不是全量监控平台：尚缺 peer-pinger 结构化接入、更多设备/下载器适配、业务外部探针、通知、完整事件关联及长期容量压测。确认这些限制之前不能宣称“所有网络与业务已监测”。
