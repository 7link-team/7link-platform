# 日期时间展示（商城资料参考）

> 来源：[datetime-format.md](/Volumes/Macintosh/Projects/aspeed/7link-mall/7link-mall-doc/src/content/docs/overview/datetime-format.md)；搬入日期：2026-10-14。
> 筛选：展示层原文，三语言日期、相对时间及数字格式参考。
> 本文是参考资料，不是本项目已采用的规则或已实现能力。尺寸、主题、技术栈、字段限制以本项目确认方案为准。
> 原文 SHA-256：`92d35a29a61d81455b5f5d3de2cce331c26923d78816be554b13af21d4d1eebb`。内嵌 Figma 转为链接；已搬入的资料互链改为本地相对路径，未搬入资料链接回源仓。

存储与传输一律 **UTC + ISO 8601**(见[统一时区](/Volumes/Macintosh/Projects/aspeed/7link-mall/7link-mall-doc/src/content/docs/overview/timezone.md));本页只管**展示层**——同一个时刻,按用户语言渲染成符合当地习惯的字符串。格式**跟界面语言走**(与 [i18n](i18n.md) 一一对应),不跟时区走。

**来源提示**
`07/14/2026`(en-US)与 `14/07/2026`(ms-MY)是**同一天**的不同顺序。**永远按 locale 格式化,禁止假设月/日次序**,否则跨语言必然读错日期。

## 三语言格式对照

| 维度 | zh-CN(简体中文) | en-US(美式英语) | ms-MY(马来语) |
|---|---|---|---|
| **短日期** | `2026/07/14` | `07/14/2026`(MM/DD/YYYY) | `14/07/2026`(DD/MM/YYYY) |
| **长日期** | `2026年7月14日` | `July 14, 2026` | `14 Julai 2026` |
| **星期** | 星期二 / 周二 | Tuesday | Selasa |
| **时间(24h)** | `14:30` | `14:30` | `14:30` |
| **时间(12h)** | 下午 2:30 | `2:30 PM` | `2:30 PTG` |
| **日期时间** | `2026/07/14 14:30` | `07/14/2026, 2:30 PM` | `14/07/2026, 2:30 PTG` |
| **周起始** | 周一 | 周日(Sunday) | 周一(Isnin) |
| **默认时制** | 24 小时 | 12 小时(AM/PM) | 12 小时(PG/PTG)或 24 小时 |

- **时制**:界面时间控件与展示默认走该语言习惯的时制;**报表 / 日志 / 对账等精确场景统一 24h**,避免 AM/PM 歧义。
- **12h 时段词**:en-US 用 `AM` / `PM`;ms-MY 用 `PG`(pagi 上午)/ `PTG`(petang 下午)/ `MLM`(malam 夜间);zh-CN 用 上午 / 下午。
- **月份 / 星期名**:en-US、ms-MY 有本地化词形(`Julai`、`Selasa`),必须走 locale 数据,不能拼英文缩写。

## 相对时间

列表、动态、消息时间戳优先用相对时间,超过阈值(如 7 天)回落到绝对短日期:

| 场景 | zh-CN | en-US | ms-MY |
|---|---|---|---|
| 刚刚 | 刚刚 | just now | baru sahaja |
| 分钟前 | 3 分钟前 | 3 minutes ago | 3 minit lalu |
| 小时前 | 2 小时前 | 2 hours ago | 2 jam lalu |
| 昨天 | 昨天 | yesterday | semalam |

## 数字与货币

- **数字千分位 / 小数**:随语言格式化(三语言均 `1,234.56`,小数点用 `.`)。
- **货币**:符号与小数位随语言**格式化**,但**币种本身由业务数据(门店结算币种)决定**,不跟界面语言走(见 [i18n](i18n.md))。马来西亚林吉特符号 `RM` 前置:`RM 12.00`。

## 实现约定

- **统一走 locale 格式化 API**(如 `Intl.DateTimeFormat(locale, options)` / ICU),**禁止手写 `YYYY-MM-DD` 拼接或硬编码月份名**。
- **单一格式化层**:日期/时间/数字/货币的格式化收敛到一处工具函数,各页只传语义(`date` / `datetime` / `time` / `relative`),不各自拼格式。
- **locale 传递**:格式化用的 locale = 用户当前界面语言(`zh-CN` / `en-US` / `ms-MY`);时区取用户[客户端时区](/Volumes/Macintosh/Projects/aspeed/7link-mall/7link-mall-doc/src/content/docs/overview/timezone.md),两者独立。
- **切换即时生效**:切语言后已渲染的时间/数字同步重排(联动 [i18n](i18n.md) 热更新)。

**来源提示**
本页定「怎么显示」;「存哪个时刻、按什么时区判定」见[统一时区](/Volumes/Macintosh/Projects/aspeed/7link-mall/7link-mall-doc/src/content/docs/overview/timezone.md),「文案资源与语言协商」见 [i18n](i18n.md)。
