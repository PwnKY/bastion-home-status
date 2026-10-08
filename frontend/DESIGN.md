# 前端视觉与边界说明（DESIGN）

本文档记录当前已实现的原型设计约定，供后续维护与接入真实数据时参考。**文档描述现状，不构成重新设计。**

## 视觉基调

- **底色：黑 / 炭灰**。`--bg #0d0f11`，侧边栏 `--sidebar #0a0c0d`，面板 `--surface #131619`，
  抬升面 `--surface-raised #191d21`，悬停 `--surface-hover #1b1f23`，分隔线 `--border #252a2f` /
  `--border-soft #20252a`。层级主要靠**底色深浅 + 1px 描边**表达，而不是阴影。
- **强调色克制使用**：鼠尾草绿 `--green #9bc5a6`（健康、当前导航、链接悬停、聚焦环）、
  琥珀 `--amber #d9ad70`（降级/观察中）、红 `--red #d98787`（故障）、蓝 `--blue #8eaac5`（维护）。
  绿/琥珀只出现在状态标记、当前项与少量描边上，大面积区域保持中性炭灰。
- **无发光效果**：全文件没有 `text-shadow` / 发光描边 / 霓虹渐变；唯一的阴影是浮层临时元素
  （toast `box-shadow: 0 8px 30px #0004`、对话框 `0 24px 100px #0006`），`dialog::backdrop` 使用
  `backdrop-filter: blur(3px)` 仅作遮罩虚化。
- **字体**：正文为本地系统字体栈（Inter → system-ui → Segoe UI → 微软雅黑），数字与标签用等宽栈
  （Cascadia Code → SFMono-Regular → Consolas），`.mono` 使用 `tabular-nums` 保证数值对齐。
  **不加载任何外部字体或 CDN 资源**，图标为内联 SVG，favicon 为本地 `public/favicon.svg`。
- 样式组织为 CSS `@layer reset, base, layout, components, responsive`，令牌集中在 `base` 层的 `:root`。

## 布局与响应式断点

固定 224px 侧边栏 + 流式工作区；内容区为 12 列风格的网格组合（统计卡、服务表、路径面板、图表）。

| 断点 | 行为 |
| --- | --- |
| `min-width: 1600px` | 增大内边距与统计卡高度，放宽服务表列宽 |
| `max-width: 1260px` | 侧边栏收窄至 198px，字号/内边距整体压缩，隐藏演示提示的次要文案 |
| `max-width: 1060px` 且 `min-width: 901px` | 主/次网格转单列，路径面板改为两列，事件列表两列 |
| `max-width: 900px` | 侧边栏改为抽屉（`transform: translateX(-100%)`），显示移动端菜单按钮与遮罩，主区取消左边距 |
| `max-width: 760px` | 全部网格单列，统计卡改两列，服务表列宽重排，边界栏改纵向堆叠 |
| `max-width: 500px` | 进一步收紧内边距与标题字号，隐藏时区等次要信息 |
| `prefers-reduced-motion: reduce` | 动画/过渡时长降为 0.01ms，滚动改为瞬时 |

页面 `min-width: 320px`，最窄移动端仍可完整操作与阅读。

## 可访问性

- 顶部 `a.skip-link` 可跳到 `#main-content`；`main` 带 `tabindex="-1"`，路由切换后聚焦主区。
- `:focus-visible { outline: 2px solid var(--green); outline-offset: 4px }` 为全站统一可见焦点环；
  搜索框使用 `:focus-within` 描边。
- 状态**不只靠颜色**：状态徽标同时给出文字标签与形状/图标（如过期显示暂停图标与“观测已过期”）。
- 语义与 ARIA：导航 `aria-current="page"`；分组/分段控件 `role="group"` + `aria-pressed`；
  图表 SVG `role="img"` + `aria-label`；搜索与筛选有 `aria-label`；移动菜单按钮 `aria-expanded`；
  不可见文本用 `.sr-only`；toast 为 `role="status" aria-live="polite"`。
- 服务详情使用原生 `<dialog>.showModal()` + `aria-labelledby`，支持 Esc 与点击遮罩关闭，并保留焦点回填。
- 路由切换、筛选更新后通过 `data-focus` 恢复焦点（含搜索框光标位置），避免键盘用户丢失位置。
- 减少动效偏好已在上表覆盖；文本与背景对比度以中性灰阶为主，避免纯黑纯白的极端对比。

## 面向未来的 API 边界

当前可以复用视觉布局和组件，但这不是生产后端的完整数据契约。真实接入还需要新增数据适配、来源声明和以下语义，不能只把演示函数换成一个 fetch 就宣称完成：

1. **数据获取层**：`src/data.js` 的 `createDemoSnapshot(scenario, now)` 是唯一的快照工厂。
   未来新增一个数据适配模块（如 `createRemoteSnapshot()`）返回**相同字段结构**的快照
   （`sampledAt` / `collector` / `services` / `metrics` / `incidents`），并验证字段、空值、时间和采集来源。
   真实来源不能保留演示场景切换与“刷新演示”行为；需要基于明确模式渲染来源声明。
2. **派生语义层**：`src/model.js` 的 `deriveView(snapshot, now)` 是纯函数，集中实现“过期即未知”
   （默认 90 秒阈值，取自 `collector.staleAfterSeconds`）、`untested` 不被时间戳改写、
   过期时当前延迟置空但保留 `reportedLatencyMs`、`unknown/untested/maintenance` 不计入可用率分母等判定。
   这些语义带有单元测试，真实数据接入后应**复用而非绕过**。当前模型使用一个统一过期阈值，
   生产版还需按服务、探针来源和检测周期判定；服务端事故状态与补传顺序也不能由前端替代。
3. **渲染层**：`src/main.js` 只消费 `deriveView` 的结果与哈希路由，**不直接发起请求**。
   引入真实数据时，轮询/取消应交由数据适配层（可参考 `collector.intervalSeconds` 的节奏），
   渲染层继续通过 `render()` 重绘，保持焦点恢复与 `aria-live` 提示行为。
4. 界面已为 7 天范围预留显式空态。后端提供 7 天历史后，需增加对应趋势的实际渲染与范围查询，
   不能仅填入 `availability7d` 就把目前的固定空态变成真实图表。
5. 原型中的“4 条就绪连接”、直连/中继文案和拓扑为合成演示映射。生产版必须读取结构化路径、
   连接数、探测窗口与有效期；不能根据一个通用 degraded 状态推断一定经 DERP。
6. 公共信息脱敏、管理认证、探测授权和凭据必须在后端实现。没有前端隐藏式的管理权限方案。

> 约束：真实接入前，前端**不得**调用真实设备、SSH、RouterOS、Headscale、DERP 或管理接口，
> 也不引入外部字体/CDN/MCP 依赖；原型保持合成数据可离线运行。
