---
title: 设计系统总览
description: 管理后台设计系统——设计基线、原子设计五阶段分层与视觉评审清单。
updates:
  - date: "2026-07-14"
    note: 初始版本建立
---

本节是 **7link 管理后台(MCH / SYS / OPS 等 Web 桌面后台)设计系统**的单一来源。技术栈:**shadcn(base-nova 风格)+ Base UI + Tailwind v4 + Design Token(CSS 变量)**。

## 分层结构

以 Brad Frost 的**原子设计(Atomic Design)**为骨架,采用工程实践中更通用的命名(保留 `Modules` 并标注其对应标准术语 `Organisms`),自下而上组合:

| 层级 | 内容 | 文档 |
| --- | --- | --- |
| **Tokens 令牌** | 色彩 / 间距 / 圆角 / 阴影 / 字体 / 动效 + **多主题(6 套品牌主题 × 明暗)**。严格说属**设计令牌体系、不属 Atomic Design**,但作为设计系统最底层基线 | [设计令牌](/overview/design/tokens/) |
| **Atoms 原子** | 不可再分的基础控件:Button、Input、Icon、Badge、Switch… | [原子](/overview/design/atoms/) |
| **Molecules 分子** | 多个原子组合成的复合控件:Card、Dialog、Popover、DropdownMenu、Pagination、UserProfile… | [分子](/overview/design/molecules/) |
| **Modules 模块** *(= Organisms)* | 面向业务场景的配置驱动组件:AutoForm、AutoTable、CRUD、Sidebar、Topbar。**对应 Atomic Design 的 Organisms** | [模块](/overview/design/modules/) · [AutoForm/AutoTable](/overview/design/auto-crud/) |
| **Templates 模板** | 页面骨架与布局(无真实数据):列表页 / 表单页 / 详情页三类骨架 | [模板](/overview/design/templates/) |
| **Pages 页面** *(可选)* | 模板结合真实业务数据形成的最终页面,属**应用层**而非组件库 | 各[端文档](/mch/)(如商户端) |

> **命名对齐**:第三层沿用工程常见的 **Modules**,同时标明它就是 Atomic Design 的 **Organisms**——既贴合团队习惯,又与标准术语建立映射。**Pages** 属应用层,不单列组件库文档,具体页面即各业务端文档。

> 另见:[组件设计清单](/overview/design/components/)(全组件索引 + 逐个设计说明)与 [AutoForm 与 AutoTable](/overview/design/auto-crud/)(后台 CRUD 高层封装,后续管理页面的实现基座)。

## 设计基线与评审清单

下表既是**设计基线**(做的时候照着做),也是**评审判定**(评的时候逐行核对)。每行给出**可判定的达标标准**与**退回信号**——命中任一退回信号即打回重做。全部值以[设计令牌](/overview/design/tokens/)为准。

| 维度 | 达标标准(可判定) | 退回信号 |
|---|---|---|
| **品牌与主题一致性** | 延续 7link 后台视觉基因,不与既有产品分叉;在 5 套预置主题(`modern`/`corporate`/`serene`/`blossom`/`brutalist`)+ `custom` 下均自洽;装饰皆有功能理由 | 出现游离于主题体系外的孤立风格;同类页面视觉不一致;纯炫技元素 |
| **色彩** | 主 / 辅 / 功能色(危险 `--destructive`、成功 `--success`、警告 `--warning`)一律经语义 Token 引用(oklch);正文对背景 **≥ 4.5:1**、大字与图形 **≥ 3:1**(WCAG AA) | `#xxxxxx` / `rgb()` 硬编码;同一语义两处取色不同;对比度不达标;换主题后功能色错位 |
| **结构(间距/圆角/边框/阴影)** | 间距为 4 / 8pt 倍数(`--spacing-unit` `0.25rem`);圆角走 `--radius` 派生工具类(sm–4xl);边框统一 `--border-width` `1px`;阴影仅用 `--shadow-sm/md/lg` 三级 | 硬编码 px 间距 / 圆角;圆角写死(`brutalist` `0rem` ↔ `blossom` `1.5rem` 必破形);随手加自定义阴影 |
| **字体与排版** | 界面走 `--font-sans`(Inter Variable)、代码 / 数字走 `--font-mono`(Geist Mono);字号 / 行高有层级、主次清晰;文本对齐与截断策略明确 | 字号混乱无层级;正文用等宽或代码用无衬线;超长文本无截断方案。字重写 `SemiBold`/`ExtraBold`(应为 `Semi Bold`/`Extra Bold`) |
| **组件复用** | 优先复用组件库(Atoms → Modules);新增组件须论证必要性并沉淀为规范 | 重复造轮子;私自 fork 组件;新增组件无必要性说明 |
| **组件状态完整** | 每个交互组件含 Default / Hover / Active / Focus / Disabled / Error **六态** | 缺任一状态(Focus / Disabled / Error 最常漏) |
| **页面状态完整** | 覆盖 空 / 加载 / 异常 / 错误提示 / 成功反馈 / 无权限 / 数据极值(超长、空值、0 值) | 只画了「数据正常」的理想态 |
| **暗黑与主题** | 每套主题的 dark 为**重新设计而非反色**,`[data-theme].dark` 下整套语义 Token 自检;渐变 / 玻璃质感不破版;新组件在**全部主题 × 明暗**下自检 | 暗色由反色得来;dark 下对比度或质感崩坏;新组件仅在默认主题验证 |
| **尺寸适配** | 所有 Web 页面覆盖 Desktop / Pad / Phone；1440×900 为桌面基准，窄屏通过抽屉、单列、卡片化和容器内滚动重排；大字号不破版，位图 2x 适配 Retina | 只交付桌面稿；整页横滚；窄屏布局塌陷、操作不可达；放大字号溢出 / 重叠；位图非 2x 发虚 |
| **动效** | 常规交互 **150–250ms** + 标准缓动;按钮按下用 `translate-y-px` 即时微反馈;有动效说明;避免过度动效与性能损耗(暂无 `--motion-*` Token,走过渡工具类) | 无节奏 / 无时长的动画;掉帧卡顿;炫技动画干扰操作 |
| **可交付成熟度**(能否进入开发) | 标注齐全(尺寸 / 间距 / 颜色均标 **Token 名**而非裸值);组件规范页整理;命名与图层结构清晰;附设计说明与开发注意事项;链接可访问 | 无标注或标注为裸数值;图层命名混乱;缺开发注意事项 |
| **可维护性 / 风险** | 不新增视觉债务;不破坏现有组件库;需成规范的沉淀为 Token / 组件;无性能 / 视觉不一致 / 上线风险 | 引入一次性样式;与既有组件冲突;埋性能或一致性隐患 |

:::note
视觉稿进入开发前必须逐行通过本表评审(联动[原型与 PRD](/overview/rules/prototype/))。
:::
