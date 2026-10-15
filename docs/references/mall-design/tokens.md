# 设计令牌（商城资料参考）

> 来源：[design/tokens.md](/Volumes/Macintosh/Projects/aspeed/7link-mall/7link-mall-doc/src/content/docs/overview/design/tokens.md)；搬入日期：2026-10-14。
> 筛选：去掉商城业务状态色；其余 Token 与主题内容保留。
> 本文是参考资料，不是本项目已采用的规则或已实现能力。尺寸、主题、技术栈、字段限制以本项目确认方案为准。
> 原文 SHA-256：`44a2d57adc35a173fc29a96263bbc73168084bd3199ec670d247534dbc4390f0`。内嵌 Figma 转为链接；已搬入的资料互链改为本地相对路径，未搬入资料链接回源仓。

所有视觉属性统一走 **Design Token**。Figma 使用 `Primitives → Semantic Color (Light/Dark) → Components` 的 alias 链，代码使用对应 CSS 变量。**禁止**在组件里硬编码色值、圆角或可复用尺寸。

当前设计系统以 **7link modern Light/Dark** 为统一基线,覆盖颜色、圆角、阴影、间距与字体。Light/Dark 共用语义 Token 名称,通过 mode 切换具体值;组件只绑定 Semantic Variables,不直接绑定模式值。

## Figma 预览

> [在 Figma 打开 Foundations](https://www.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall?node-id=53-2)。预览包含 44 个 primitive、62 个 Light/Dark semantic colors、36 个 dimensions、16 个 typography styles 和 5 个 shadow styles。

[查看来源 Figma 组件预览](https://embed.figma.com/design/HPojH5xVmyhO4KJeE3XFTH/7link-mall-web-ALL?embed-host=7link-mall-doc&node-id=53-2&footer=false&page-selector=false)

## 语义色彩(Light)

| Token | modern/light | 用途 |
|---|---|---|
| `--background` | `#fafafa` | 页面背景 |
| `--foreground` | `#171717` | 正文文字 |
| `--primary` | `#171717` | 主色(主按钮、强调) |
| `--primary-foreground` | `#fafafa` | 主色上的文字 |
| `--secondary` / `--muted` / `--accent` | `#f5f5f5` | 次要、弱化、悬停背景 |
| `--muted-foreground` | `#737373` | 次要文字 |
| `--border` / `--input` | `#e5e5e5` | 边框 / 输入框边框 |
| `--ring` | `#a3a3a3` | 焦点环 |

## 功能色

| Token | modern/light | 语义 |
|---|---|---|
| `--destructive` | `#ef4444` | 危险 / 删除 / 错误 |
| `--success` | `oklch(0.62 0.17 150)` / computed `#03a14a` | 成功 |
| `--warning` | `oklch(0.70 0.16 75)` / computed `#d48e00` | 警告 |

> 侧栏(`--sidebar-*`)、图表(`--chart-1..5`)拥有独立成套 Token,随主题切换。

## 间距 / 圆角 / 边框 / 阴影

| Token | 值 | 说明 |
|---|---|---|
| `--spacing-unit` | `0.25rem` (`4px`) | 基准单位；Figma 覆盖 `0.5` 步长的 `2px` 以及常用 `1`–`16` 倍数 |
| `--border-width` | `1px` | 统一边框粗细 |
| `--radius` | `0.875rem` (`14px`) | modern 圆角基准 |
| `--radius-sm` → `--radius-4xl` | `radius × 0.6` → `× 2.6` | 派生圆角层级(sm/md/lg/xl/2xl/3xl/4xl) |
| Figma `radius/full` | `9999px` | pill / circle 固定圆角值 |
| `--shadow-sm` | `0 1px 2px 0 rgb(0 0 0 / .05)` | 轻阴影 |
| `--shadow-md` | `0 4px 6px -1px rgb(0 0 0 / .08), 0 2px 4px -2px rgb(0 0 0 / .04)` | 中阴影(卡片/弹层) |
| `--shadow-lg` | `0 10px 15px -3px rgb(0 0 0 / .08), 0 4px 6px -4px rgb(0 0 0 / .04)` | 重阴影(对话框/浮层) |

## 字体

| Token | 字体 | 用途 |
|---|---|---|
| `--font-sans` | Inter Variable | 界面正文与标题 |
| `--font-mono` | Geist Mono Variable | 代码 / 数字 / 编号 |
| `--font-arabic` | Noto Sans Arabic | 阿拉伯语场景回退 |
| `Typography/Label/tight` | Inter Medium 14/14 | `Label` 的 `text-sm font-medium leading-none` 标准样式 |
| `Typography/Badge/default` | Inter Semi Bold 12/16 | 标准 Badge 与业务状态 |
| `Typography/Badge/compact` | Inter Semi Bold 10/16 | 18px 稠密表格计数 |

> Inter 字重命名注意:`Semi Bold`(非 SemiBold)、`Extra Bold`(非 ExtraBold)。

## 动效 Motion

当前未沉淀专用 `--motion-*` 变量;动效走 **CSS 过渡工具类**,并遵循[设计基线](design-system.md)约定:

- **明确 ease / duration**,避免过度动效、避免影响性能。
- 常规交互(hover / 展开 / 关闭)时长约 **150–250ms**,标准缓动;按钮按下用 `translate-y-px` 即时微反馈。
- 页面切换逻辑连贯,有动效说明文档。

> 若后续多处复用固定时长/缓动,再论证沉淀为 `--duration-*` / `--ease-*` Token——避免过早抽象。

## 多主题边界

业务组件的完整设计基线仍是 `modern` Light/Dark。ThemeSwitcher 另有专用 `Theme Preview` Variables collection,只用于主题选择器的 compact/card 缩略图,不能被业务组件直接引用,也不代表其他主题已经完成全站适配。

| Mode | Background | Primary | Text | Border | Radius |
|---|---|---|---|---|---|
| Modern | `#fafafa` | `#171717` | `#171717` | `#e5e5e5` | `14px` |
| Business | `#fafaf9` | `#6366f1` | `#1e1b4b` | `#e0e7ff` | `12px` |
| Eye Care | `#f5f0e6` | `#5a7c5a` | `#2d3b2a` | `#c8d9c8` | `8px` |
| Pink Kawaii | `#fff0f6` | `#e8388a` | `#5c002e` | `#ffd6e7` | `24px` |
| Neo Brutalism | `#fef08a` | `#ff006e` | `#000000` | `#000000` | `0px` |
| Custom | `#fafafa` | `#6366f1` | `#171717` | `#e5e5e5` | `14px` |

ThemePreview Atom 对 collection 设置显式 mode,ThemeSwitcher compact/grid 只实例化 ThemePreview。其他主题进入业务规范前,仍必须完成完整 Variables、组件状态与屏幕验收,不能只替换色值后宣称兼容。

## 明暗模式

`[data-theme="modern"].dark` 重定义整套语义 Token，而不是把 Light 简单反色。Figma 的 `Semantic Color` collection 使用同一批变量的 `Light` / `Dark` modes，上层组件只绑定 semantic token。

| Token | Light | Dark |
|---|---|---|
| `--background` | `#fafafa` | `#0a0a0a` |
| `--foreground` | `#171717` | `#fafafa` |
| `--card` | `#ffffff` | `#171717` |
| `--primary` | `#171717` | `#fafafa` |
| `--border` | `#e5e5e5` | `#262626` |
| `--destructive` | `#ef4444` | `#f87171` |

**来源提示**
新增视觉需求先找现成 Token；确无合适 Token 再论证是否沉淀新 Token。任何新组件必须在 modern Light/Dark 下自检，并且所有视觉属性继续绑定 Variables。
