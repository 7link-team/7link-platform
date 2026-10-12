# Raft（Botiverse）对比调研

> 2026-10-12。只读调研：raft.build、docs.raft.build（Getting Started、Work on Raft、Tutorials、Features 25 页、Developers 4 页）、定价页、botiverse GitHub（raft-source、raft-computer-installer、oar、raft-external-agents、raft-artifact-share-action、desktop-beta）。未登录应用。【事实】带来源，【推断】为判断。

## 1. 一句话

Raft 是「Slack + 任务看板」式的人机对等工作区。强项在 agent 运行时这一层：唤醒 / 收件箱协议、生命周期、记忆、可自动运行的协作日常。交付治理几乎空白：只有一级 `in review → done` 人工批准；没有预览、终端、项目、需求、缺陷。

## 2. 功能清单【事实】

| 领域 | 要点 | 来源 |
|---|---|---|
| Server | 最高层容器（频道、agent、computer、任务、文件都属于它）；rail：Search / Chat / Tasks / Members / Computers / Settings；Owner / Admin / Member，agent 可为 Member 或 Admin；入会协议、onboarding agent | /features/server/ |
| 消息 | 公开 / 私有频道，默认 #all；单层 thread；DM 支持人↔人、人↔agent、agent↔agent 与小群；消息不可编辑删除；Convert to task；Joint Channel（实验，跨 ≤30 个 server，只同步消息与附件） | /features/messaging/ |
| 任务 | 任务 = 带元数据的消息；todo → in progress → in review → done（+closed）；认领而非指派；进入 done 需人批准；agent 提出拆分方案、人批准后开工 | /features/collaboration/tasks/、/divide-the-work/ |
| 文件与批注 | 单个 ≤50 MB；内联预览图片 / PDF / Markdown / 文本 / CSV / 视频；**锚定批注**：Markdown 段落、代码行区间、CSV 行、HTML 区域、视频时间点，批注成为带回链的 thread 消息 | /features/collaboration/files/、/comments/ |
| Agent | 名字、描述、runtime、模型；每个 agent 在所在机器有持久工作目录（记忆、笔记、仓库），创建者与管理员可在 UI 查看；四色状态点；三种重置（Restart / Session reset / Full reset）；唤醒：新消息、@、agent 自设 Reminder（锚定 thread）；特权操作生成 action card 由人确认；agent 可经 API 建 agent（无配额） | /features/agents/ |
| Runtime | Claude Code、Codex CLI、Cursor CLI、Grok Build、Kimi Code、OpenCode、Pi、自带 key；订阅与 key 用户自持；同一 server 可混用 | /features/agents/runtime/ |
| Computer | 一条安装命令 + 设备登录；Windows 仅 WSL；负责连接、启停 / 休眠 / 唤醒 agent、投递消息、崩溃恢复；多机；agent 暂不可迁移；Socket.IO；本地凭据代理让 agent 拿不到 server 凭据；Rust 安装器哈希校验与回滚 | /features/server/computers/、GitHub |
| 开发者 | agent 收件箱是唯一事实来源；唤醒提示不带内容（轮询 / 提示流 / HMAC webhook）；游标确认；写入时若会话已有更新消息返回 `state:"interrupted"`，带幂等键重试；`raft-agent-status.v1` 五态；SDK 结果带 `next` 与 `text`；`RAFT_OPERATIONS` 操作清单（schema、副作用、幂等、所需能力）；Raft Apps（OAuth、Marketplace、webhook） | /developers/sdk/ |
| 通知与追赶 | 浏览器推送（DM 必推、已加入频道、关注的 thread）；Activity（All / Unread / Mentions），跳到第一条未读；Saved；⌘K 搜索（不搜文件内容）；Web / PWA / Android / iOS TestFlight / macOS | /catch-up-in-one-place/ 等 |
| 定价 | Free：消息保留 30 天；Pro：$8.80 / 席位 / 月（年付），人 1 席、agent 0.1 席；Enterprise 规划私有部署与 SSO | /pricing |
| 预览 / 终端 | 文档未提供运行中服务的远程预览或 Web 终端；另有 raft-artifacts.com 发布单文件（≤10 MiB） | raft-artifact-share-action |

## 3. 信息架构

- 【事实】server 切换器 + rail；侧栏 Activity / Saved / Pinned / Channels / Direct Messages / Computers（agent 挂在各自 computer 下）；频道内 Chat / Tasks / Files 三个标签；agent 详情 Profile / Activity / Chat / Reminders / Workspace / Apps。
- 【推断】以频道为容器，没有项目层；我们是项目为主容器，多一层。

## 4. 对比

**Raft 有、我们缺或更弱：** agent 唤醒与收件箱协议（过期上下文写入的处理）；给模型读的操作清单与 `next`；agent 生命周期细节；锚定批注；Reminder 与「loop 契约」；一条命令接入 + onboarding agent；跨组织共享频道；完整的追赶闭环（Activity、跳到第一条未读、Saved）；特权操作统一 action card。

**我们有、Raft 没有：** 四级完成阶梯与结构化证据、平台重跑、跨模型家族测试 / 验收 agent、隐藏验收集、穿透追踪；项目、目标 → 计划 → 报告闭环、领域模板、需求 / 缺陷 / 用例 / 流水线；有配额的 agent 自建；远程预览、产物查看器、终端、文件管理、k8s 与原生 Windows；甲方门户与超容差变更确认；指派、依赖、截止。

## 5. 建议（待用户决定是否采纳）

| 优先级 | 建议 | 依据 |
|---|---|---|
| P0 | runner / agent 协议加入「收件箱 + 不带内容的唤醒提示 + 游标确认 + interrupted 重试」，防止多 agent 基于过期上下文写入 | /developers/sdk/、wake-and-status |
| P0 | agent 操作清单：每个操作的 schema、副作用、幂等、所需能力，结果返回 `next`；权限与配额按能力落实 | `RAFT_OPERATIONS` |
| P0 | 产物查看器加入锚定批注（代码行、表格行、HTML 区域、视频时间点、小说段落），批注挂到 L3 / L4 验收与缺陷 | /comments/ |
| P1 | 规格定义 agent 生命周期：三种重置、五态状态、工作区与记忆查看权限 | /lifecycle/、/workspace/ |
| P1 | 周期任务与报告加「loop 契约」（节奏、自检、预算、升级条件、输出格式），由锚定 thread 的提醒驱动 | investing tutorial |
| P1 | 甲方门户之外加「甲方联合会话」：跨租户、成员边界只读、不共享看板；变更仍走门户结构化流程 | Joint Channels |
| P2 | 所有 agent 特权操作统一呈现为决策卡，人确认一次 | action card |
| P2 | 一条命令 + 设备登录接入，自动检测 runtime，onboarding agent，首次跑通 ≤10 分钟 | /meet-your-onboarding-agent/ |
| P2 | 会话补齐 Activity（全部 / 未读 / 提及，任务 thread 显示阶梯级别）、跳到第一条未读、收藏；默认「成员即订阅」 | /catch-up-in-one-place/ |
| P3 | 定价参考「人 1 席、agent 0.1 席」 | /pricing |

**局限：** 截图只有 alt 文本，IA 来自文字与官网样图；computer 与 server 的具体协议未公开（仅提到 Socket.IO）；文档内部有几处不一致（指派、HTML 预览、agent 能否做 Admin 操作）。
