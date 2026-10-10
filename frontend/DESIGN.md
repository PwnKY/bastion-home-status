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

## 真实 API 边界

1. `src/api.js` 验证版本化摘要；默认真实 API，失败显示未知。演示仅在显式演示构建中可选，生产构建禁用。
2. 后端负责认证、状态确认、事件及补传顺序；纯 `deriveView` 只做最后的新鲜度保护，按服务和来源分别判定。
3. 实时年龄采用服务器时间加浏览器单调计时，避免用户墙钟偏差。过期撤销当前延迟，保留历史上报参考。
4. 7 天读取独立历史端点，缺失点保留未知，SVG 断线，不以 24h 冒充 7d，不连接数据缺口。
5. 就绪连接数、路径模式使用结构化观测；缺失时显示未知。结构图是示意，不代表节点逐个已验证。
6. 浏览器只访问同域只读摘要，无设备管理接口或采集凭据。采集认证不代替浏览器登录；入口访问策略独立配置。

> 保留黑色 / 炭灰视觉和可访问性约定。不得添加外部字体/CDN/MCP，不能以本机健康宣称端到端业务已通过。
