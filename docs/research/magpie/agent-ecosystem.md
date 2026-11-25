# magpie × aigate：「Agent 生态 / 开发者工具 / 平台集成」能力盘点

审计日 2026-11-25。**只读**：未修改任何仓库，未运行 magpie 或其测试，未运行 aigate；所有机制结论来自本次读到的源码与文档文本。

- **magpie 基线**：`/tmp/magpie-study`，HEAD `e66d872a8eaecc285b0d597917d6b520035712ec`。引用形如 `路径:行号@e66d872`，均为本次实际读到的行。
- **aigate 基线**：工作树 `ded2312`（分支 `claude/agent-skills-setup`，基于 main `41f66e4`）。工作树比 main 多 1 个提交（`AGENTS.md` 的「Agent skills」段 + `docs/agents/*`）；main 现为 `9fd8dd6`，再多 5 个提交（PR #35，仅任务/TTS 鉴权修复与架构正文同步，经 `gh api compare/41f66e4...main` 核对，不改变本审计结论）。引用形如 `路径:行号`。
- **判定口径**：已支持 / 部分支持 / 未支持 / 不适用 / 设计上刻意不做 / 值得评估，定义按任务给定。补充约定：主判定只取一类；**▲** 表示该行存在值得评估的缺口（主判定为未支持或部分支持的行也可带 ▲），▲ 行统一在 §3 展开。
- **与同目录其他稿件的关系**：网关 / 路由 / 供应商侧见 [gateway-routing.md](gateway-routing.md)，界面侧见 [gui-ux.md](gui-ux.md)，总览见 [README.md](README.md)。本稿覆盖 agent 生态、开发者工具、平台集成与工程实践。

## 0. 结论先行

1. magpie 的「agent 生态」主体（93 个内置 agent 适配器、配置改写与所有权、Library 分发、Skill/MCP router、Memory、Artifacts、Fast tools、会话迁移）全部是**单用户本机工具形态**，对服务端多租户网关**不适用**；aigate 没有也不应该有对应实现。
2. magpie 的「平台集成」主体（任意插件/脚本/MCP 执行、插件市场与插件页面）被 aigate 规格**明确排除**（038 §10、041 §1、phase-roadmap §4.1/§5.2）。
3. aigate 已有等价物的只有三项：上游网络守卫（SSRF/DNS 重绑定）、Compose 单机部署、默认不记录正文；部分支持的主要是备份（缺定时与封存加密）、条件并发（DB revision CAS 有、HTTP ETag 无）、图片/视频（仅 mock）、来源证明（无发布签名/SBOM）、`/v1/models` 元数据与接入文档。
4. 真正值得产品决策的是：**控制台多语言**（041 把 USD 付款定为商业首发必需，而控制台只有中文）、**计量化 Web Search 工具**、**租户级出站脱敏**、**`/v1/models` 元数据与按客户端接入指南**、**发布签名/构建证明/SBOM**。
5. 工程实践上 aigate 最该补的三件事：修复的「兄弟面扫描 + 旧代码必失败」验收条款、规则化复盘文件、`AGENTS.md` 的失效指针与非目标/排障路由。main 上 PR #35 在 18 分钟内连续 4 个鉴权修复，正是 magpie 代码标准第一条所警告的模式。

---

## 1. 汇总表

### 1.1 Agent 接线

| 编号 | 能力 | magpie | aigate | 判定 |
| --- | --- | --- | --- | --- |
| 1.1 | 多 agent 适配矩阵 | `onMachineAll` 字面量 93 个内置构造器，另加 profile/多目录/WSL 孪生/插件 agent 动态行（`internal/agent/agents.go:321-416@e66d872`、`internal/agent/agents.go:258-262@e66d872`、`internal/agent/agents.go:287-289@e66d872`）；README 列 90 个名字（`README.md:86@e66d872`） | 无。全仓不识别、不改写任何客户端 agent 配置 | 不适用 |
| 1.2 | 已安装 agent 发现 | 配置文件→目录→应用签名→PATH 命令；skill 安装器残留目录、同名外部命令不算（`internal/agent/agent.go:466-488@e66d872`；`docs/subsystems/agent-wiring.md:244-246@e66d872`） | 无 | 不适用 |
| 1.3 | 原子改写 agent 配置 | 只改目标键，注释/顺序/缩进保留（`internal/edit/file.go:1-3@e66d872`）；临时文件+rename、保 mode/组/BOM/符号链接、多硬链接原地写、内容不变不写（`internal/edit/file.go:124-218@e66d872`、`internal/edit/file.go:86-100@e66d872`、`internal/edit/file.go:234-276@e66d872`）；多文件失败回滚（`internal/edit/file.go:332-380@e66d872`） | 无。aigate 不写用户本机文件，配置经 HTTP + 数据库 CAS | 不适用 |
| 1.4 | 所有权标记与漂移/还原 | `# magpie: …` 独立注释行（`internal/edit/mark.go:9-30@e66d872`）；`applied.json`/`stash.json` 记录写入值，`Drift` 报告 unwired/replaced/unreachable/proxied/bypassed，断开与卸载还原（`docs/subsystems/agent-wiring.md:21-25@e66d872`、`docs/subsystems/agent-wiring.md:64@e66d872`、`docs/subsystems/agent-wiring.md:142@e66d872`） | 无 | 不适用 |
| 1.5 | agent CLI 版本检测与更新 | 按安装方式（self/npm/bun/pnpm/brew）更新，读不出就只显示版本（`internal/agent/cliupdate.go:3-9@e66d872`、`internal/agent/cliupdate.go:295-330@e66d872`、`internal/agent/cliupdate.go:925-951@e66d872`） | 无 | 不适用 |
| 1.6 | WSL 内 agent 接线 | 只探测运行中的发行版，NAT/mirrored 网络下地址与 key 跟随（`docs/subsystems/agent-wiring.md:44-46@e66d872`、`docs/subsystems/agent-wiring.md:228@e66d872`） | 无 | 不适用 |
| 1.7 | 关闭 agent 厂商遥测 | 写各 agent 的官方遥测开关（`docs/subsystems/agent-wiring.md:18@e66d872`） | 无 | 不适用 |
| 1.8 | 导入链接（"Add to magpie"） | 供应商链接 `magpie://import?…`，用户确认后才保存（`internal/provider/importurl.go:10-40@e66d872`；`docs/reference.md:3418-3456@e66d872`）；客户端应用链接，先查系统是否注册了 scheme 处理器（`internal/agent/applink.go:28-58@e66d872`） | 未支持。只有复制 Key（`web/src/views/KeysView.vue:307-309`）、Playground curl 片段（`web/src/views/PlaygroundView.vue:54-61`）、README curl/SDK 示例（`README.md:177-228`） | **值得评估** ▲（低） |
| 1.9 | 按客户端归属用量 | key 写作 `magpie-<agent>` 或按 UA 首词计入 Usage（`docs/integrating.md:44-52@e66d872`；`internal/agent/agents.go:32-40@e66d872`） | 不解析 User-Agent（`internal/` 无相关代码）；用量按租户/Key 归属 | 不适用 |
| 1.10 | `/v1/models` 富元数据 | `display_name`/reasoning levels/`context_window`/`modalities`/pricing，`?format=`、`?kind=`（`docs/integrating.md:54-70@e66d872`） | 仅 `id/object/owned_by/created` 四字段（`internal/server/gateway.go:25-28`、`internal/server/gateway.go:44-53`） | 部分支持 ▲（低） |
| 1.11 | 面向开发者的接入指南 | 三级接入路径 + 适配器 PR 要求（`docs/integrating.md:1-12@e66d872`、`docs/integrating.md:115-159@e66d872`） | README「API 示例」+ OpenAI SDK 示例（`README.md:177-228`）；无按客户端（Claude Code/Codex/Cline…）的配置指南 | 部分支持 |

### 1.2 Library

| 编号 | 能力 | magpie | aigate | 判定 |
| --- | --- | --- | --- | --- |
| 2.1 | 写一次分发到各 agent（instructions/MCP/skills/hooks/subagents/commands/rules） | `Library.sync` 逐 agent 按其自身格式写入（`internal/library/sync.go:90-128@e66d872`；`docs/subsystems/library.md:1-14@e66d872`） | 无。`instructions`/`systemInstruction` 只是客户端请求字段原样转发（`internal/provider/native_request.go:43`、`internal/provider/gemini_request.go:53`） | 不适用 |
| 2.2 | 只删除自己写的内容 | `Applied` 记录写过的 instructions hash、MCP、skills、hooks、subagents 等，被用户改过的即视为用户的（`internal/library/library.go:154-197@e66d872`；标记 `internal/library/instructions.go:26-27@e66d872`） | 无 | 不适用（模式可借鉴，见 §4.4） |
| 2.3 | 项目级库（按仓库/工作树分发） | `docs/subsystems/library.md:26@e66d872` | 无 | 不适用 |
| 2.4 | 借用远程实例的 skills/prompts | `docs/subsystems/library.md:21@e66d872` | 无 | 不适用 |

### 1.3 Skill router / memory / MCP router

| 编号 | 能力 | magpie | aigate | 判定 |
| --- | --- | --- | --- | --- |
| 3.1 | Skill router | 一个 `magpie-skills` 入口 skill 列出其余 skill，按需读取，提示词只带一份描述；为 Codex/Pi/Droid/dsh 写 stub，为 Claude Code 写 Read 规则与命令（`docs/subsystems/skill-router.md:3@e66d872`、`docs/subsystems/skill-router.md:9-17@e66d872`；`internal/library/skill_router.go:66-70@e66d872`、`internal/library/skill_router.go:191@e66d872`、`internal/library/skill_router.go:348@e66d872`、`internal/library/skill_router.go:448@e66d872`） | 无 | 不适用 |
| 3.2 | Memory（共享记忆：MCP server + digest 注入 + 手工编辑） | `docs/subsystems/memory.md:3-7@e66d872`、`docs/subsystems/memory.md:76-136@e66d872`、`docs/subsystems/memory.md:153-184@e66d872`；`internal/memory/store.go:3-22@e66d872` | 无 | 不适用 |
| 3.3 | 记忆蒸馏/夜间整理/语义检索 | `internal/memdistill/memdistill.go:1-12@e66d872`；`internal/memembed/memembed.go:1-19@e66d872`；`docs/subsystems/memory.md:214-399@e66d872` | 无。且 041 非目标含「用额外 LLM 读取客户正文」（`specs/041-profit-hybrid-platform/spec.md:31`） | 不适用 |
| 3.4 | MCP router | 每 agent 一个 `magpie` 入口；工具超 10 个改给 `find_tools`/`call_tool`；`run_code` 代码模式；每次调用受 key 限额约束（`docs/subsystems/mcp-router.md:3@e66d872`、`docs/subsystems/mcp-router.md:12-16@e66d872`、`docs/subsystems/mcp-router.md:21@e66d872`；`internal/gateway/hub_mcp.go:33-56@e66d872`；`internal/gateway/hub_ondemand.go:44@e66d872`；`internal/gateway/hub_code.go:32-51@e66d872`） | 无。「任意插件/脚本/MCP 执行环境」为非目标（`specs/038-new-api-capability-migration/spec.md:201`；`docs/design/phase-roadmap.md:120`、`docs/design/phase-roadmap.md:162`） | 设计上刻意不做 |
| 3.5 | MCP OAuth 代签 | `internal/mcpauth/mcpauth.go:1-16@e66d872` | 无（随 MCP 执行一并排除） | 设计上刻意不做 |
| 3.6 | Fast tools（Antigravity 批量工具插件） | `docs/subsystems/fast-tools.md:3-7@e66d872`、`docs/subsystems/fast-tools.md:11-39@e66d872` | 无 | 不适用 |

### 1.4 隐私脱敏

| 编号 | 能力 | magpie | aigate | 判定 |
| --- | --- | --- | --- | --- |
| 4.1 | 规则层脱敏 + 稳定占位符 + 流式还原 | 规则找密钥/个人数据/自定义词（`internal/redact/redact.go:1-17@e66d872`、`internal/redact/redact.go:37-100@e66d872`）；占位符 `{{KIND_哈希8位}}` 同值同形（`internal/redact/redact.go:835@e66d872`）；流式逐事件还原，跨事件被拆开的占位符扣住再拼（`internal/redact/writer.go:9-18@e66d872`）；值只存内存 | 未支持。数据面不做任何 PII/密钥脱敏；仓库里的「脱敏」只指日志与资格探测输出（`docs/architecture/README.md:41`；`docs/research/extended-operations.md:30`） | **值得评估** ▲ |
| 4.2 | 模型层脱敏（本地模型） | AstrLink Guard（39MB，进程内）与 Privacy Filter（1.6GB，独立进程）（`internal/pii/pii.go:1-16@e66d872`）；等待上限与降级（`internal/masking/masking.go:176-262@e66d872`） | 无 | 不适用 |
| 4.3 | fail closed | 要遮蔽而无 masker，或 masker 失败 → `ErrUnmasked`/panic → 503，不外发（`internal/gwmw/gwmw.go:4-16@e66d872`、`internal/gwmw/gwmw.go:285-319@e66d872`） | 无遮蔽点可施加；fail closed 作为原则已用于商业链（`specs/041-profit-hybrid-platform/spec.md:152`） | 未支持 |
| 4.4 | 脱敏审计记录与试算 | `Preview`（`internal/masking/masking.go:418-426@e66d872`）；记录只含 kind/长度/首字符，不含值（`internal/gwmw/gwmw.go:132-137@e66d872`、`internal/gwmw/gwmw.go:150-158@e66d872`） | 无 | 未支持 |

### 1.5 网关内工具

| 编号 | 能力 | magpie | aigate | 判定 |
| --- | --- | --- | --- | --- |
| 5.1 | Web search 聚合 | 10 家搜索 API（`internal/provider/search_api.go:92-110@e66d872`）；多 key 轮换或 `pick=order`、被拒 key 垫后、可关闭可 mix（`internal/provider/search_api.go:32-36@e66d872`、`internal/provider/search_api.go:52-55@e66d872`） | 未支持（全仓无联网搜索实现） | **值得评估** ▲ |
| 5.2 | web_fetch | 搜索 MCP server 的 `web_fetch` 工具（`README.md:279@e66d872`）；抓取受 netguard 约束（`internal/netguard/netguard.go:1-7@e66d872`） | 未支持。无任意 URL 抓取 | **值得评估** ▲（与 5.1 捆绑） |
| 5.3 | 作为 MCP server 暴露给 agent | `/mcp/magpie/` 下的 image、vision、web-search、memory、sessions、artifacts、tools（`internal/gateway/search_mcp.go:38-47@e66d872`） | 未支持。规格只排除「MCP 执行/Agent 工作流」，未明确覆盖「aigate 作为 MCP 服务端」（`docs/design/phase-roadmap.md:120`、`docs/design/phase-roadmap.md:162`） | 未支持（待产品决策） |
| 5.4 | 图像生成 | `/v1/images/generations` + `/mcp/magpie/image`（`internal/gateway/image_mcp.go:31-45@e66d872`；`README.md:280@e66d872`） | `POST /v1/images/generations` 已接媒体账务链与不可变 artifact，但只有 mock adapter（`docs/architecture/README.md:635`、`docs/architecture/README.md:1377`；`specs/038-new-api-capability-migration/capability-matrix.md:46`） | 部分支持 |
| 5.5 | 视频生成 | `/v1/videos`，Grok 订阅或 MiniMax（`internal/gateway/video.go:34-40@e66d872`） | `/v1/tasks` 的 `video_generation` 仅 deterministic mock（`specs/038-new-api-capability-migration/capability-matrix.md:46`；`docs/architecture/README.md:39`） | 部分支持 |
| 5.6 | 视觉理解 | `VisionMCPPath`（`internal/gateway/search_mcp.go:41@e66d872`） | 未支持；多模态后置（`README.md:67`；`specs/038-new-api-capability-migration/capability-matrix.md:19`） | 未支持 |

### 1.6 插件系统

| 编号 | 能力 | magpie | aigate | 判定 |
| --- | --- | --- | --- | --- |
| 6.1 | 内置插件运行时 | `pluginrt`：Manifest（ID/API 范围/Requires/Provides/Essential/InUse/Apply），按依赖排序应用，失败隔离（`internal/pluginrt/pluginrt.go:1-10@e66d872`、`internal/pluginrt/pluginrt.go:24-94@e66d872`；`docs/subsystems/plugin-runtime.md:25-31@e66d872`） | 无。「任意插件执行环境」不属于目标（`docs/design/phase-roadmap.md:120`） | 设计上刻意不做 |
| 6.2 | 网关扩展点（observer/路由策略/中间件/工具/预算） | 五个点，各有失败策略（`docs/subsystems/plugin-runtime.md:181-269@e66d872`） | 无。路由/计量/结算「只能由核心模块实现，不能进入普通插件」（`docs/design/phase-roadmap.md:162`） | 设计上刻意不做 |
| 6.3 | Bun 插件宿主与 provider 插件 | Bun 1.4.2 按 SHA-256 下载（`internal/plugin/bun.go:35-38@e66d872`）；stdin/stdout JSON-RPC 与流控（`internal/plugin/host.js:1-22@e66d872`；`internal/plugin/store.go:1-15@e66d872`） | 无 | 设计上刻意不做 |
| 6.4 | JS 网关中间件 | 进程内 JS 引擎，fail open（`internal/middleware/middleware.go:1-14@e66d872`） | 无。「任意脚本」为非目标（`specs/041-profit-hybrid-platform/spec.md:33`） | 设计上刻意不做 |
| 6.5 | 插件工具（经 MCP 暴露） | `docs/subsystems/plugin-tools.md:9-16@e66d872`、`docs/subsystems/plugin-tools.md:29-41@e66d872` | 无 | 设计上刻意不做 |
| 6.6 | 插件页面/面板/菜单项 | 沙箱 iframe + postMessage + 声明式读取（`docs/subsystems/plugin-runtime.md:284@e66d872`、`docs/subsystems/plugin-runtime.md:289-437@e66d872`） | 无。041 明确「不 iframe、不跳转」（`specs/041-profit-hybrid-platform/spec.md:165`） | 设计上刻意不做 |
| 6.7 | 插件 agent 适配器 | `internal/agentplug/agentplug.go:1-28@e66d872` | 无 | 设计上刻意不做 |
| 6.8 | 注册表/市场/Discover | registry.json + npm 搜索（`internal/plugin/market.go:29-51@e66d872`）；npm 镜像来源优先级（`internal/plugin/registry.go:23-69@e66d872`） | 无 | 设计上刻意不做 |
| 6.9 | 插件计数遥测 | `internal/stats/plugins.go:22-30@e66d872` | 无外发遥测 | 不适用 |

### 1.7 同步与备份

| 编号 | 能力 | magpie | aigate | 判定 |
| --- | --- | --- | --- | --- |
| 7.1 | 便携备份包（口令封存） | AES-256-GCM + PBKDF2-SHA256 60 万轮（`internal/backup/backup.go:1-15@e66d872`、`internal/backup/backup.go:53-55@e66d872`） | 离线备份：pg_dump + key 材料 + SHA256SUMS；包本身不加密，依赖加密存储（`scripts/backup.sh:15-35`；`docs/runbooks/backup-restore.md:7-20`）；恢复演练 PASS（`docs/verification/backup-restore.md:3-14`） | 部分支持 |
| 7.2 | 定时自动备份与保留 | 默认每 24 小时、保留 7 份（`docs/subsystems/sync-backup.md:251-286@e66d872`） | 未支持。人工按手册执行，无调度/保留，无 WAL 归档（`docs/runbooks/backup-restore.md:22`） | 未支持 ▲（低） |
| 7.3 | 跨机配置同步（WebDAV/S3/加密 GitHub 仓库） | 读版本 + 条件替换 + 合并 + 预览 + 撤销（`docs/subsystems/sync-backup.md:141-177@e66d872`、`docs/subsystems/sync-backup.md:203-249@e66d872`、`docs/subsystems/sync-backup.md:478-539@e66d872`） | 无。事实源是单一 PostgreSQL，HA/灾备属 025 且未冻结（`specs/025-production-ha/spec.md:3`） | 不适用 |
| 7.4 | 条件替换协议（ETag） | `version{ETag,Modified}` → `If-None-Match`/`If-Modified-Since`；PUT 被拒即 `errChanged` 后重合并（`internal/davsync/dav.go:22-62@e66d872`） | 数据库层 revision CAS（`expected_revision`/`management_revision`，60 个非测试文件；例 `internal/catalog/channel_operations.go:111-137`、`internal/catalog/channel_operations.go:280`）；HTTP 层无 ETag/If-Match/412 | 部分支持 |
| 7.5 | 细粒度同步开关（记忆/用量/登录态） | `docs/subsystems/sync-backup.md:203-222@e66d872`、`docs/subsystems/sync-backup.md:403-476@e66d872` | 无 | 不适用 |
| 7.6 | 数据目录迁移 | `docs/subsystems/sync-backup.md:358-401@e66d872` | 无 | 不适用 |

### 1.8 Artifacts

| 编号 | 能力 | magpie | aigate | 判定 |
| --- | --- | --- | --- | --- |
| 8.1 | Artifacts 发布/版本/沙箱服务 | MCP 六个工具（`docs/subsystems/artifacts.md:50-70@e66d872`）；`<id>/v<N>/` 不可变版本（`internal/artifact/store.go:1-15@e66d872`）；CSP sandbox、仅 loopback（`docs/subsystems/artifacts.md:95-123@e66d872`） | 无。`media_result_artifacts` 只是图片/TTS 结果的不可变存储（`internal/server/media_artifacts.go:30-79`），不是 agent 产物 | 不适用 |
| 8.2 | 分享到 usemagpie.ai | 用户主动分享，站点只存 token 哈希（`internal/artifact/share.go:21-32@e66d872`；`docs/subsystems/artifacts.md:345-397@e66d872`） | 无 | 不适用 |

### 1.9 运维与分发

| 编号 | 能力 | magpie | aigate | 判定 |
| --- | --- | --- | --- | --- |
| 9.1 | 安装/卸载脚本 | 一行安装脚本；卸载先把 agent 配置还原（`README.md:58@e66d872`） | 无。从源码经 Compose 部署（`README.md:141`） | 不适用 |
| 9.2 | 自更新与多渠道 | 站点 feed→GitHub 回退（`internal/update/source.go:5-13@e66d872`；`internal/update/update.go:1-10@e66d872`）；镜像与代理（`internal/update/mirror.go:12-23@e66d872`）；Daily/Stable 通道（`docs/reference.md:3653-3740@e66d872`） | 无自更新；升级/回滚按手册（`docs/runbooks/deployment.md:102`） | 不适用 |
| 9.3 | 发布签名与构建证明 | ed25519 签 SHA256SUMS（`internal/update/signed.go:13-31@e66d872`）+ Sigstore SLSA 构建证明（`internal/update/attest.go:27-60@e66d872`） | 有第三方来源证明（`third_party/provenance.json`；`make check` 含 `upstream-provenance-check`）与镜像 source 标签（`compose.yaml:36-39`）；自有制品无签名/证明/SBOM（`docs/architecture/README.md:98`） | 部分支持 ▲ |
| 9.4 | mTLS / 内置 CA / HTTPS | `internal/mtls/mtls.go:1-15@e66d872`；`internal/gateway/https.go:20-39@e66d872` | 未支持。TLS 由外部反代终止，应用在 loopback 明文监听（`deploy/nginx.conf:5-17`；`docs/architecture/README.md:1272-1282`；`docs/runbooks/deployment.md:54`） | 未支持 ▲（低） |
| 9.5 | 出口区域检测 | `internal/egress/egress.go:1-13@e66d872` | 无 | 不适用 |
| 9.6 | 网络守卫（SSRF/特殊网段） | `internal/netguard/netguard.go:1-7@e66d872`、`internal/netguard/netguard.go:9-36@e66d872` | 已有：上游网络策略校验 scheme/主机/端口/CIDR，拨号时对解析地址复核，拒绝元数据/链路本地/私网/保留段（`internal/provider/network.go:43-47`、`internal/provider/network.go:107-132`、`internal/provider/network.go:135-150`；测试 `internal/provider/network_test.go:47`、`internal/provider/network_test.go:81`、`internal/provider/network_test.go:115`；`docs/architecture/README.md:1260`） | 已支持（范围限上游端点） |
| 9.7 | OTLP 导出 | `internal/otlp/otel.go:41-46@e66d872`；`docs/subsystems/plugin-runtime.md:181-196@e66d872` | 仅 loopback `/metrics` 四项计数（`internal/server/server.go:121`、`internal/server/server.go:647-659`）与结构化日志（`internal/telemetry/telemetry.go:18-25`）；对外 OTel 导出列为后续（`docs/research/ecosystem-overseas.md:78`） | 未支持 ▲（低） |
| 9.8 | Docker | 多阶段构建，带 node/npm/uv 运行时（`Dockerfile:1-41@e66d872`） | `Dockerfile:1-48` + `compose.yaml`；Compose 单机为必须完成项（`docs/design/phase-roadmap.md:111`）；容器运行验收表述不一致（`docs/architecture/README.md:43` 与 `docs/architecture/README.md:1378`，见 §5） | 已支持（待核实） |
| 9.9 | 远程实例/网关级联 | 远程实例作为 provider（`internal/provider/remote_magpie.go:69@e66d872`；`README.md:272@e66d872`） | 个人多设备形态不适用；多跳级联被 041 明确拒绝（`specs/041-profit-hybrid-platform/spec.md:136`）；单跳外部上游已有（035） | 设计上刻意不做 |
| 9.10 | TLS 证书热加载/导入 | `internal/gateway/tls.go:19-29@e66d872` | 无（反代终止 TLS） | 不适用 |
| 9.11 | 保活 | `internal/awake/awake.go:1-16@e66d872` | 无 | 不适用 |
| 9.12 | 推送通知（Bark 等） | `internal/bark/bark.go:1-6@e66d872` | 仅站内告警（`docs/architecture/README.md:1375`、`docs/architecture/README.md:1141`）；外发通知明确后置（`specs/038-new-api-capability-migration/capability-matrix.md:40`；`specs/024-extension-platform/plan.md:36`） | 未支持 ▲（已规划） |
| 9.13 | 会话迁移 | `internal/sessions/relocate_claude.go:16-25@e66d872`；`docs/session-relocation.md:1-12@e66d872` | 无 | 不适用 |

### 1.10 遥测与隐私政策

| 编号 | 能力 | magpie | aigate | 判定 |
| --- | --- | --- | --- | --- |
| 10.1 | 每日使用上报 | PostHog：随机 id、版本、系统、agents/providers/models 的内部 id、插件与 MCP 市场使用、合作伙伴计数（`internal/stats/stats.go:1-12@e66d872`、`internal/stats/stats.go:71-150@e66d872`；`docs/reference.md:4724-4771@e66d872`） | 无外发遥测；用量事实在 PostgreSQL，经营报表供运营方自查（`docs/architecture/README.md:178`、`docs/architecture/README.md:1366`） | 不适用 |
| 10.2 | 开关与 DO_NOT_TRACK、源码构建不发 | `Off()`：`DO_NOT_TRACK`/`MAGPIE_NO_STATS`/设置项（`internal/stats/stats.go:46-53@e66d872`）；`Run` 只在正式版发送（`internal/stats/stats.go:58-67@e66d872`） | 不适用（无遥测） | 不适用 |
| 10.3 | 合作伙伴点击计数与图标代理 | `docs/reference.md:4780-4798@e66d872` | 无 | 不适用 |
| 10.4 | 默认不记录 prompt/输出 | 同样不记正文（`README.md:299@e66d872`） | 日志记录类型只有 request_id/阶段/错误码/耗时四个字段（`internal/telemetry/telemetry.go:18-25`；测试 `internal/telemetry/telemetry_test.go:149`）；`docs/architecture/README.md:1264` | 已支持 |

### 1.11 工程实践（细节见 §4）

| 编号 | 能力 | magpie | aigate | 判定 |
| --- | --- | --- | --- | --- |
| 11.1 | 子系统设计参考（职责/运行路径/约束/验证命令；同 PR 更新；评审规则） | `docs/subsystems/README.md:1-51@e66d872` | 统一架构 + HLD/LLD + SDD 覆盖职责与边界（`AGENTS.md:3-20`）；没有逐子系统的「验证命令 + 评审规则」页 | 部分支持 |
| 11.2 | 代码标准（变更/测试/验收/评审清单） | `docs/code-standards.md:8-39@e66d872`、`docs/code-standards.md:41-125@e66d872`、`docs/code-standards.md:163-204@e66d872` | `AGENTS.md:22-27`、`CONTRIBUTING.md:11-16`、`.github/pull_request_template.md` 有不变量与验证口径；无评审清单，无「旧代码上必须失败」条款 | 部分支持 |
| 11.3 | 复盘机制 | `LESSONS.md:1-9@e66d872`、`LESSONS.md:1278-1318@e66d872` | 未支持。有 ADR 与逐片验证/失败分析（`docs/decisions/ADR-002-implementation-refinements.md`；`docs/verification/acceptance-workers-2-clock-analysis.md`），但没有规则化的复盘闭环 | 未支持 |
| 11.4 | 社区 PR 治理（先查危害） | `docs/contributors.md:8-25@e66d872`、`docs/contributors.md:54-76@e66d872` | `CONTRIBUTING.md:58-62` 有 PR 说明要求；无「先查危害」清单；PR 不作为请求面（`docs/agents/issue-tracker.md:32`） | 不适用 |
| 11.5 | 测试风格 | 真实字节 fixture、沙箱 HOME、不触网、`-race -count=20` 判 flake、守卫测试（`docs/code-standards.md:28-39@e66d872`、`docs/code-standards.md:60-90@e66d872`） | deterministic mock/fixture + 专用 PostgreSQL + 029 拒绝必需 SKIP（`CONTRIBUTING.md:46-56`）；前端单引擎单语言（`web/tests`，55 个文件） | 部分支持 |

### 1.12 多语言

| 编号 | 能力 | magpie | aigate | 判定 |
| --- | --- | --- | --- | --- |
| 12.1 | UI 多语言（en/zh/zh-TW/ja/de；按插件拆分；多语言测试） | 键为英文原文，缺失回退英文（`internal/gui/assets/i18n.js:1-5@e66d872`；共 22710 行，zh@6、zh-TW@5616、ja@11210、de@16806）；系统语言推断（`internal/uilang/uilang.go:14-28@e66d872`）；多语言同占位符测试（`docs/code-standards.md:91-105@e66d872`） | 未支持。`<html lang="zh-CN">`（`web/index.html:2`）、Element Plus 固定 zh-cn（`web/src/main.ts:3`、`web/src/main.ts:11`）、日期按 zh-CN（`web/src/format.ts:33`）、后端错误文案中文硬编码（如 `internal/server/server.go:651`）；多语言明确后置（`specs/038-new-api-capability-migration/capability-matrix.md:40`） | 未支持 ▲ |
| 12.2 | CLI/TUI/托盘/通知本地化 | `internal/uilang/uilang.go:1-5@e66d872` | 无对应界面形态 | 不适用 |

### 1.13 判定统计

| 判定 | 数量 | 行号 |
| --- | --- | --- |
| 已支持 | 3 | 9.6、9.8（待核实）、10.4 |
| 部分支持 | 10 | 1.10、1.11、5.4、5.5、7.1、7.4、9.3、11.1、11.2、11.5 |
| 未支持 | 10 | 4.3、4.4、5.3、5.6、7.2、9.4、9.7、9.12、11.3、12.1 |
| 不适用 | 34 | 1.1–1.7、1.9、2.1–2.4、3.1–3.3、3.6、4.2、6.9、7.3、7.5、7.6、8.1、8.2、9.1、9.2、9.5、9.10、9.11、9.13、10.1–10.3、11.4、12.2 |
| 设计上刻意不做 | 11 | 3.4、3.5、6.1–6.8、9.9 |
| 值得评估 | 4 | 1.8、4.1、5.1、5.2 |
| **合计** | **72** | |
| 带 ▲ 的行（含值得评估与缺口） | 11 | 1.8、1.10、4.1、5.1、5.2、7.2、9.3、9.4、9.7、9.12、12.1 |

---

## 2. 逐项细节

### 2.1 Agent 接线

**magpie 做什么**

- **数量**：README 的「Supported agents」列 90 个名字（`README.md:86@e66d872`）。代码里 `onMachineAll` 的字面量是 93 个构造器（`internal/agent/agents.go:323-415@e66d872`）：按名称逐一对照，多出的三个是 DS Bot、Obsidian 和 Antigravity 桌面应用（README 的「Antigravity CLI」对应的是 `agy`）。此外还动态追加 omp 的 profile、QwenPaw 的子 agent、Hermes 的 profile、Claude Code 的多目录行（`internal/agent/agents.go:416@e66d872`），以及 WSL 孪生和插件提供的 agent（`internal/agent/agents.go:258-262@e66d872`、`internal/agent/agents.go:287-289@e66d872`）。任务背景里说的「约 100 个」略高于实际。
- **每个 agent 是什么**：一个 `Agent` 值，声明 `Path`/`Dir`/`Bin` 用于发现，若干 `Field`（各有 `Get`/`Set`/`Options`）用于改写，加一组可选钩子（`Notice`、`Sync`、`Unwire`、`Join`、`Check`、`Import`、`Launch` 等）（`internal/agent/agent.go:98-128@e66d872` 及其后的结构体定义）。
- **发现**：`Detected` 依次看配置文件（且必须是该 agent 自己写的）、目录（只含 `skills`/`logs`/`debug` 等残留的不算）、应用签名、PATH 上的命令；CLI 没了但目录还在的标为 `CLIMissing`（`internal/agent/agent.go:466-488@e66d872`、`internal/agent/agent.go:686-699@e66d872`；`docs/subsystems/agent-wiring.md:244-246@e66d872`）。
- **原子改写**：`internal/edit` 的包契约是「只改请求的键；注释、顺序和缩进保持原样」（`internal/edit/file.go:1-3@e66d872`）。`WriteAtomic` 写同目录临时文件再 rename，保留权限与组，跟随符号链接写目标，多硬链接文件改原地写，字节相同则完全不写（避免无谓唤醒 watcher），保留 UTF-8 BOM（`internal/edit/file.go:124-218@e66d872`、`internal/edit/file.go:86-100@e66d872`、`internal/edit/file.go:234-276@e66d872`）。`Atomically` 让一次涉及多个文件的编辑失败时整体回滚（`internal/edit/file.go:332-380@e66d872`）。README 对用户的承诺是「配置被原子改写，注释、顺序和格式保持原样」（`README.md:113@e66d872`）。
- **所有权标记**：magpie 写进 TOML/YAML/dotenv 的每一行上方加一条独立的 `# magpie: <是什么>; <怎么撤销>` 注释，注释随行增删，用户自己的行不打标（`internal/edit/mark.go:9-30@e66d872`；`docs/subsystems/agent-wiring.md:142@e66d872`）。改写前的值存进 `stash.json`，写入值记进 `applied.json`；`Drift` 把「别人改了配置」分成 unwired/replaced/unreachable/proxied/bypassed，Disconnect 与卸载按 stash 还原（`docs/subsystems/agent-wiring.md:21-25@e66d872`）。总原则写在约束第一条：「magpie 只删除自己写过的内容」（`docs/subsystems/agent-wiring.md:64@e66d872`）。
- **版本与更新**：从二进制所在路径判断安装方式，用对应的更新器（自带更新命令或 npm/bun/pnpm/Homebrew）；判断不出就只显示版本（`internal/agent/cliupdate.go:3-9@e66d872`、`internal/agent/cliupdate.go:295-330@e66d872`、`internal/agent/cliupdate.go:925-951@e66d872`）。WSL 内的 CLI 同样处理（`docs/subsystems/agent-wiring.md:46@e66d872`）。
- **WSL**：每个运行中的发行版探测一次，绝不为了列出而启动已停止的发行版；NAT 与 mirrored 两种网络下地址和 key 各自跟随（`docs/subsystems/agent-wiring.md:44@e66d872`、`docs/subsystems/agent-wiring.md:228@e66d872`）。
- **导入链接**分两类：
  - 供应商链接 `magpie://import?preset=…&key=…&chat=…`，打开后先展示将添加的名称、端点和模型，按 Add 才保存（`internal/provider/importurl.go:10-40@e66d872`；`docs/reference.md:3418-3456@e66d872`）。
  - 客户端应用链接（Lody、AionUi、Cherry Studio、Cindy）：这些应用把供应商存在数据库里，magpie 不写，只生成应用自己的 scheme 链接让用户在应用内确认；`OpenAppLink` 先问系统有没有注册该 scheme 的处理器，没有就报错而不是静默无效（`internal/agent/applink.go:11-27@e66d872`、`internal/agent/applink.go:28-58@e66d872`；`docs/subsystems/agent-wiring.md:313-470@e66d872`）。

**aigate 证据**

- 全仓（排除 node_modules、dist、`.claude/`）没有 `ANTHROPIC_BASE_URL`、`OPENAI_BASE_URL`、`.codex/config`、`CLAUDE_CONFIG_DIR` 等引用；唯一出现的 `cc-switch` 是 2026-11-10 人工从本机 CC Switch 读出端点后用环境变量喂给 `scripts/provider-qualification` 的探测记录（`docs/architecture/README.md:41`；脚本本身只接收 `--base-url/--api-key`，`scripts/provider-qualification/main.go:46-50`）。
- 客户接入路径只有三条：创建 Key 后复制明文（`web/src/views/KeysView.vue:307-309`、`web/src/views/KeysView.vue:412`）；Playground 按当前请求生成 curl 片段，密钥用环境变量 `MONI_API_KEY`、地址取 `window.location.origin`（`web/src/views/PlaygroundView.vue:54-61`、`web/src/views/PlaygroundView.vue:149`）；README 的 curl 与 OpenAI Python SDK 示例（`README.md:177-228`）。
- `/v1/models`（API Key 入口）固定返回 `id/object/owned_by/created`（`internal/server/gateway.go:25-28`、`internal/server/gateway.go:44-53`）；带 `context_limit`、`supports_tools`、`retail_price` 的富投影只给控制台会话（`internal/server/queries.go:144-162`）。
- 不解析 User-Agent，也没有按客户端归类的用量维度。

**判定理由**：1.1–1.7、1.9 是「改写用户本机 agent 配置」这一产品形态的组成部分，服务端多租户网关既没有访问客户文件的条件，也不应承担。1.8、1.10、1.11 是 aigate 面向开发者客户时可补的缺口，成本低，见 §3。

### 2.2 Library

**magpie 做什么**

- Library 把 instructions、MCP servers、skills、hooks、subagents、slash commands、rules 放在一处，`sync` 时逐个 agent 调用 `syncInstructions`/`syncMCP`/`syncHooks`/`syncEnv`/`syncSubagents`/`syncCommands`/`syncRules` 写成该 agent 自己的格式（`internal/library/sync.go:90-128@e66d872`；`docs/subsystems/library.md:1-14@e66d872`）。
- 「只收回自己写的」靠 `Applied`：记录写过的 instructions 片段哈希、MCP 条目、skills、hooks 条目原文、subagents/commands/rules 文件哈希等；文件被用户改过（哈希不同）就算用户的（`internal/library/library.go:154-197@e66d872`）。instructions 里 magpie 的部分夹在 `<!-- magpie:begin … -->` 与 `<!-- magpie:end -->` 之间（`internal/library/instructions.go:26-27@e66d872`）。
- 还能按项目（git 仓库及其工作树）分发，并把写入物登记进仓库的 `info/exclude` 以免进 git（`docs/subsystems/library.md:26@e66d872`）。

**aigate 证据**：没有「平台管理 instructions 并注入请求」的功能。请求里的 `instructions`、`systemInstruction` 只是协议字段，按 profile 白名单校验后原样转发（`internal/provider/native_request.go:43`、`internal/provider/native_request.go:89`；`internal/provider/gemini_request.go:53`、`internal/provider/gemini_request.go:65`）。

**判定理由**：不适用。这是单用户本机的 agent 文件分发。

### 2.3 Skill router / memory / MCP router

**magpie 做什么，各解决什么问题**

- **Skill router** 解决「装的 skill 越多，提示词越胀」：开启后每个 agent 的 skills 目录只放一个 `magpie-skills`，它的 SKILL.md 列出其余 skill 的描述与路径，agent 遇到匹配任务再去读。为保留各 agent 的斜杠选择器，给 Codex/Pi/Droid/dsh 写 stub，给 Claude Code 写 `permissions.allow` 的 Read 规则和 `/name` 命令文件（`docs/subsystems/skill-router.md:3@e66d872`、`docs/subsystems/skill-router.md:9-17@e66d872`；`internal/library/skill_router.go:66-70@e66d872`、`internal/library/skill_router.go:191@e66d872`、`internal/library/skill_router.go:348@e66d872`、`internal/library/skill_router.go:448@e66d872`）。
- **Memory** 解决「在 Claude Code 说的偏好，Codex 不知道」：记忆是一个 Markdown 文件一条，带 scope/kind/origin/status 等 front matter（`internal/memory/store.go:3-22@e66d872`）；通过三种方式使用——每个 agent 可挂载的 memory MCP server（`memory_search`/`memory_save` 等，`docs/subsystems/memory.md:76-136@e66d872`）、写进 agent instructions 的 digest（预算 global 4000 字符等，且只在发布时变化以保住 prompt cache，`docs/subsystems/memory.md:153-184@e66d872`）、以及 Memory 页面。蒸馏用模型读已结束的会话提取候选记忆，夜间整理合并重复、归档过期，语义检索用本地嵌入模型（`internal/memdistill/memdistill.go:1-12@e66d872`；`docs/subsystems/memory.md:214-399@e66d872`）。
- **MCP router** 解决「同一个 MCP server 每个 agent 各起一份、每个 agent 各签一次 OAuth」：开启后每个 agent 只拿到一个名为 `magpie` 的条目，指向 `/mcp/magpie/hub/<agent>`，由 magpie 统一连接各 server。工具超过 10 个的 agent 改拿 `find_tools`/`call_tool` 两个按需工具；`run_code` 让模型用一段脚本批量调用工具（至多 100 次调用、50 秒、堆 512MB）；每次调用先过调用方 key 的限额（`docs/subsystems/mcp-router.md:3@e66d872`、`docs/subsystems/mcp-router.md:12-16@e66d872`、`docs/subsystems/mcp-router.md:21@e66d872`；`internal/gateway/hub_mcp.go:33-56@e66d872`、`internal/gateway/hub_mcp.go:452-456@e66d872`；`internal/gateway/hub_ondemand.go:44@e66d872`；`internal/gateway/hub_code.go:32-51@e66d872`）。

**aigate 证据**

- 三者都没有实现（全仓无 MCP、无 skill、无 memory 相关代码）。
- 规格排除：「签到、邀请奖励、分佣、任意插件/脚本/MCP 执行环境」列为 038 非目标（`specs/038-new-api-capability-migration/spec.md:198-204`）；「任意插件执行环境、MCP 执行、脚本」在能力分类里是「不属于目标」（`docs/design/phase-roadmap.md:120`）；024「不增加任意脚本执行、Go 动态插件、MCP 工具执行器或通用工作流」（`specs/024-extension-platform/spec.md:18`）。
- 记忆蒸馏会让平台的另一个 LLM 读取客户会话正文，这与 041 的非目标「用额外 LLM 读取客户正文做任务分类、质量评估或影子双生成」（`specs/041-profit-hybrid-platform/spec.md:31`）方向一致地被限制；且 aigate 的姿态是不记录 prompt 与输出（`docs/architecture/README.md:1264`）。

**判定理由**：Skill router、Memory、Fast tools 是 agent 客户端侧能力，不适用。MCP router 与 MCP 代签依赖 MCP 执行环境，属设计上刻意不做。需留意一点：规格用词是「MCP 执行」「Agent 工作流」，**aigate 自己作为 MCP 服务端对外提供工具**没有被逐字覆盖（见 §5）。

### 2.4 隐私脱敏

**magpie 做什么**

- **规则层**（`internal/redact`）：找 API key、私钥、token、连接串密码，可选个人数据（邮箱、手机、校验和有效的证件与卡号），以及用户自定义词和规则（`internal/redact/redact.go:1-17@e66d872`、`internal/redact/redact.go:37-100@e66d872`）。
- **稳定占位符**：形如 `{{API_KEY_k3v9x2mq}}`，由值的带密钥哈希生成，同值永远同占位符，因此会话历史、prompt cache 和 thinking 签名保持有效；值只存内存；遮过一次的值此后在任何上下文里再现都再遮（`internal/redact/redact.go:4-17@e66d872`、`internal/redact/redact.go:835@e66d872`）。
- **流式还原**：`Writer` 逐事件把占位符换回原值；一个占位符被拆在两个事件之间时扣住前一事件等待拼接，不增删字节；工具调用参数同样还原（`internal/redact/writer.go:9-18@e66d872`）。
- **模型层**（`internal/pii`）：规则找不到的名字、地址、裸密码交给本地模型。AstrLink Guard 39MB 在进程内跑；Privacy Filter 1.6GB 放在独立进程 `pf-serve`，理由是不让 1.7GB 常驻网关进程、GPU 驱动崩溃不连坐（`internal/pii/pii.go:1-16@e66d872`）。用户显式开启的模型读不完或失败时 panic 扣住请求；默认开启的模型超时则只用规则层的结果（`internal/masking/masking.go:176-262@e66d872`）。
- **fail closed**：设置要求遮蔽而没有 masker 服务，或 masker 失败，请求得到 503「nothing was sent」，供应商收不到任何内容（`internal/gwmw/gwmw.go:4-16@e66d872`、`internal/gwmw/gwmw.go:285-319@e66d872`）。整个功能默认关闭（`internal/masking/masking.go:29-31@e66d872`）。
- **可核对**：`Preview` 试算；`Teller` 只报每个被遮值的 kind、长度、首字符（`internal/masking/masking.go:418-426@e66d872`；`internal/gwmw/gwmw.go:132-137@e66d872`、`internal/gwmw/gwmw.go:150-158@e66d872`）。

**aigate 证据**

- 数据面没有任何请求改写或脱敏。仓库内「脱敏」只出现在两处含义：资格探测输出只含脱敏摘要（`docs/architecture/README.md:41`）；早期研究把「观测平台的 URL 凭据脱敏」转为日志/错误出口的需求（`docs/research/extended-operations.md:30`）。
- fail closed 作为原则在商业链上已有（「授权、数据库、报价、凭据或 Guard 失败 fail closed」，`specs/041-profit-hybrid-platform/spec.md:152`），但没有遮蔽点可以施加。

**判定理由**：4.1 值得评估（见 §3.3）；4.2 的本地模型与 aigate「不引入原生组件、服务端多租户」的形态不符，不适用；4.3、4.4 随 4.1 才有意义，当前未支持。

### 2.5 网关内工具

**magpie 做什么**

- **Web search**：模型不会搜索时，由会搜索的 provider 或搜索 API 代答 `web_search`。内置 10 家搜索 API：Tavily、Brave、Exa、Firecrawl、You.com、DeepSeek、豆包（火山引擎）、元宝（腾讯云）、TinyFish、SearXNG（`internal/provider/search_api.go:92-110@e66d872`）。每家可存多个 key：默认轮换，或 `pick=order`（先用第一个，被拒再换），最近被拒的排最后；可整体关闭而不丢 key；可标记 mix 并发多家合并结果（`internal/provider/search_api.go:32-50@e66d872`、`internal/provider/search_api.go:52-55@e66d872`）。
- **web_fetch**：搜索同时作为 MCP server 暴露，带 `web_search` 和 `web_fetch` 两个工具（`README.md:279@e66d872`）；抓取任意 URL 受 netguard 约束（`internal/netguard/netguard.go:1-7@e66d872`）。
- **MCP server 家族**：网关自己服务的 MCP server 有 image、vision、web-search、memory、sessions、artifacts、tools 七个（`internal/gateway/search_mcp.go:38-47@e66d872`）。
- **图像/视频**：`/mcp/magpie/image` 与 `/v1/images/generations`，图片作为 MCP 图片内容返回并保留一小时（`internal/gateway/image_mcp.go:31-45@e66d872`）；`/v1/videos` 采用 OpenAI Videos API 形状，由 Grok 订阅或 MiniMax 提供（`internal/gateway/video.go:34-40@e66d872`）。

**aigate 证据**

- 没有联网搜索与网页抓取实现。
- 图片生成：`POST /v1/images/generations` 已接入媒体 Reserve/Attempt/Observe/Settle、钱包和不可变 artifact，开发环境用 mock provider；真实 adapter 未完成（`docs/architecture/README.md:635`、`docs/architecture/README.md:39`、`docs/architecture/README.md:1377`）。
- 视频：`/v1/tasks` 的 `video_generation` 只有 deterministic mock 的账务路径（`specs/038-new-api-capability-migration/capability-matrix.md:46`）。
- 视觉/多模态：未覆盖（`README.md:67`；`specs/038-new-api-capability-migration/capability-matrix.md:19`）。
- 媒体不参与动态路由，单 profile 单 adapter（`docs/architecture/README.md:1371`）。

**判定理由**：5.4、5.5 是同一能力的不同完成度（HTTP 入口与账务链已有，供应商适配与资格未完成），判部分支持。5.1、5.2 值得评估。5.6 是路线图上的后置项，当前未支持。5.3 待产品决策。

### 2.6 插件系统

**magpie 做什么**

- **内置插件运行时** `pluginrt`：magpie 自己的功能也拆成 `plugins/` 下的包，通过 `Register(Manifest)` 注册；Manifest 声明宿主 API 范围、`Requires`/`Provides` 的服务、是否 Essential、`InUse`（用户是否已有依赖它的数据）、`Apply`；运行时按依赖顺序应用，状态有 off/pending/active/failed/disposed，一个失败不影响其他（`internal/pluginrt/pluginrt.go:1-10@e66d872`、`internal/pluginrt/pluginrt.go:24-94@e66d872`）。当前内置 15 个插件包（`plugins/` 目录：all、budget、gateway、groups、library、middleware、otel、redact、rotation、routing、search、sessions、tools、usage、warmups）。
- **五个网关扩展点**：observer（只读观测）、路由策略（只排序、fail open）、中间件（JS 中间件 fail open；遮蔽 masker fail closed）、工具、预算（限额缺失则 503）（`docs/subsystems/plugin-runtime.md:181-269@e66d872`）。
- **社区插件**跑在 Bun 宿主（首次需要时下载，校验 SHA-256；`internal/plugin/bun.go:35-38@e66d872`），宿主与 magpie 之间是 stdin/stdout 的一行一条 JSON RPC，带字节窗口流控（`internal/plugin/host.js:1-22@e66d872`）。provider 插件负责登录、列模型、发请求（`internal/plugin/store.go:1-15@e66d872`）。
- **中间件**不走 Bun 而在进程内 JS 引擎执行，理由是每个流事件都要过一遍（`internal/middleware/middleware.go:1-14@e66d872`）。
- **工具**：插件的 `tool` 钩子经 `/mcp/magpie/tools` 提供给所有 agent，每次调用受 key 限额约束、55 秒超时（`docs/subsystems/plugin-tools.md:9-16@e66d872`、`docs/subsystems/plugin-tools.md:29-41@e66d872`）。
- **插件自己的页面**：`magpie.page` 声明一个 HTML，在沙箱 iframe（`allow-scripts`，不 `allow-same-origin`）里跑，经 postMessage 与窗口通信，只能读取它声明过的只读数据（`docs/subsystems/plugin-runtime.md:284@e66d872`、`docs/subsystems/plugin-runtime.md:289-400@e66d872`）；还可以往 magpie 自己的页面里加面板和菜单项（`docs/subsystems/plugin-runtime.md:402-437@e66d872`）。
- **市场**：社区仓库的 registry.json（内置一份副本）加 npm 搜索；国内镜像开关只在官方地址失败时或明确开启后启用（`internal/plugin/market.go:29-51@e66d872`；`internal/source/source.go:1-5@e66d872`）。
- **遥测**：每日上报里带「Discover 列出的插件中装了哪些、哪些开着」，供市场显示使用人数（`internal/stats/plugins.go:22-30@e66d872`）。

**aigate 证据**

- 无任何插件机制。规格的排除是明确且多处的：038 非目标（`specs/038-new-api-capability-migration/spec.md:201`）；041 非目标（`specs/041-profit-hybrid-platform/spec.md:33`）；能力分类「不属于目标」（`docs/design/phase-roadmap.md:120`）；「以下能力只能由核心模块实现，不能进入普通插件：路由选择、计量、预占、结算、钱包与 journal 写入、采购成本记录、恢复。插件、动态脚本、MCP 执行和 Agent 工作流不属于前三阶段」（`docs/design/phase-roadmap.md:162`）；024 不增加任意脚本执行（`specs/024-extension-platform/spec.md:18`）。
- 插件页面所依赖的 iframe 形态也被 041 明确排除：「一个 aigate Vue SPA，一个同源 API；不 iframe、不跳转、不加载 New API/Sub2API 前端资源」（`specs/041-profit-hybrid-platform/spec.md:165`）。

**判定理由**：设计上刻意不做。可借鉴的只有「扩展点 + 每个点声明失败策略」这个结构，见 §4.4。

### 2.7 同步与备份

**magpie 做什么**

- **备份包**：把 providers、settings、profiles、各 agent 的模型选择、library 打成一个 JSON，用口令派生密钥（PBKDF2-SHA256，60 万轮）以 AES-256-GCM 封存；登录态和用量历史默认不带（`internal/backup/backup.go:1-15@e66d872`、`internal/backup/backup.go:53-55@e66d872`）。
- **同步**：把同一个包放到 WebDAV、S3 或私有 GitHub 仓库，多台机器之间合并而非覆盖。远程接口收敛成「读版本 + 条件替换」：WebDAV/S3 用 ETag（没有则 Last-Modified），GitHub 用 blob SHA；读用 `If-None-Match`，写带版本条件，被拒说明别的机器刚写入（`errChanged`），按同一合并流程重试（`internal/davsync/dav.go:22-62@e66d872`）。同步前可预览（带 token，计划变了就拒绝执行），同步后可撤销（`docs/subsystems/sync-backup.md:203-249@e66d872`）。GitHub 仓库必须是私有的，大包分片上传，单次同步文件总量上限 64 MiB（`docs/subsystems/sync-backup.md:478-539@e66d872`）。
- **定时自动备份**：默认每 24 小时写一份到本机目录、保留 7 份，带校验和与修剪（`docs/subsystems/sync-backup.md:251-286@e66d872`）。

**aigate 证据**

- 备份：`scripts/backup.sh` 要求停流并持有维护锁，产出 `pg_dump` 自定义格式 dump、key 材料文件、manifest 和 SHA256SUMS，目录 0700、密钥 0600；脚本写明 `contains_plaintext_key_material=true`（`scripts/backup.sh:15-35`）。手册要求把整个 bundle 放进加密且访问受控的存储，「SHA256 用于完整性检查，备份来源本身仍需可信」（`docs/runbooks/backup-restore.md:9-20`）。恢复演练在 2026-11-10 通过，包含篡改 bundle 拒绝（`docs/verification/backup-restore.md:3-14`）。
- 没有定时调度、保留策略和 WAL 归档：「停机窗口与备份频率决定实际 RPO，脚本没有 WAL 持续归档能力」（`docs/runbooks/backup-restore.md:22`）。
- 条件并发：aigate 的写入普遍带 `expected_revision`，SQL 里以 `WHERE … AND management_revision=$4` 实现，冲突返回 409 后只读核查（`internal/catalog/channel_operations.go:111-137`、`internal/catalog/channel_operations.go:280`）。HTTP 层没有 ETag/If-Match/412。041 的管理写设计提到「版本/ETag」作为未来开放窄类型写的前提（`specs/041-profit-hybrid-platform/spec.md:173`）。
- 无跨机配置同步；HA 与灾备属 025，「完整生产/HA 仍为独立草案，未冻结未验收」（`specs/025-production-ha/spec.md:3`）。

**判定理由**：备份的核心（可验证的离线备份与恢复）已具备并有演练证据，但相对 magpie 的「封存 + 定时」缺两个子能力，判部分支持。跨机同步针对的是「同一用户的配置分布在多台机器」，aigate 是单库事实源，不适用。条件替换在数据库层有等价物，协议层没有，判部分支持。

### 2.8 Artifacts

**magpie 做什么**：agent 通过 MCP 工具 `publish_artifact`/`read_artifact`/`artifact_data`/`list_artifacts`/`open_artifact`/`delete_artifact` 发布页面（`docs/subsystems/artifacts.md:50-70@e66d872`）；每个版本写入 `<id>/v<N>/` 后不再改动（`internal/artifact/store.go:1-15@e66d872`）；页面在 CSP sandbox（不给 `allow-same-origin`）里以不透明源运行，只有本机 loopback 可访问（`docs/subsystems/artifacts.md:95-123@e66d872`）。用户可以主动把某个 artifact 分享到 usemagpie.ai，之后的每个新版本自动跟上；站点只保存 token 的哈希（`internal/artifact/share.go:21-32@e66d872`；`docs/subsystems/artifacts.md:345-397@e66d872`）。

**aigate 证据**：无。`media_result_artifacts` 是图片/TTS 结果按租户、用户、Key、原请求校验后读取的不可变存储（`internal/server/media_artifacts.go:30-79`），与「agent 生成的交互页面」不是一回事。

**判定理由**：不适用。它是 agent 客户端侧的产物形态，且依赖 MCP；「分享到厂商站点」与多租户商业网关无关。

### 2.9 运维与分发

**magpie 做什么**

- **安装与卸载**：一行安装脚本；Mac 构建签名并公证，每个发布文件带 GitHub Actions 构建证明（`README.md:58@e66d872`）。卸载先把各 agent 配置还原，再拆除自己装的东西（`docs/subsystems/agent-wiring.md:25@e66d872`）。
- **自更新**：更新源是站点 feed，到不了再回退 GitHub（`internal/update/source.go:5-13@e66d872`；`internal/update/update.go:1-10@e66d872`）；可配置镜像前缀与代理，镜像只是传输通道，文件仍按 feed 的哈希校验（`internal/update/mirror.go:12-23@e66d872`）；有 Every/Daily/Stable 三个通道，Stable 是发布满三天的版本（`docs/reference.md:3653-3740@e66d872`）。
- **签名与构建证明**：发布 CI 用 ed25519 私钥签 `SHA256SUMS`，公钥内置在二进制里，feed 只是信使（`internal/update/signed.go:13-31@e66d872`）；在此之上再校验 Sigstore 的 SLSA 构建证明（证书链、Rekor 条目、issuer、工作流与仓库身份、文件摘要），证明存在但校验失败即拒绝，取不到则记为未证明但放行（`internal/update/attest.go:27-60@e66d872`）。
- **mTLS / HTTPS / TLS**：magpie 自建 CA，给设备签发客户端证书，网关与 `magpie web` 对其他计算机以 HTTPS 提供服务（`internal/mtls/mtls.go:1-15@e66d872`）；没有自己证书时可导入，或由内置 CA 签一张（`internal/gateway/https.go:20-39@e66d872`）；`--tls-cert` 的证书文件变化时自动重读（`internal/gateway/tls.go:19-29@e66d872`）。
- **出口与网络守卫**：egress 通过 Cloudflare trace 页判断当前出口是否在供应商不支持的地区（`internal/egress/egress.go:1-13@e66d872`）；netguard 是「对非 magpie 自选的 URL 下载可以连到哪里」的唯一规则，拒绝 loopback、私网、链路本地、CGNAT、基准测试段等，并处理 FakeIP 与 NAT64（`internal/netguard/netguard.go:1-7@e66d872`、`internal/netguard/netguard.go:9-36@e66d872`）。
- **OTLP**：作为 observer 挂在网关上，导出请求、账本行和会话；资源属性只带 `service.name` 与用户显式设置的，不采集主机信息（`internal/otlp/otel.go:41-46@e66d872`；`docs/subsystems/plugin-runtime.md:181-196@e66d872`）。
- **远程实例**：另一台机器上的 magpie 可以作为 provider，笔记本用它桌面机的供应商与账号（`README.md:272@e66d872`；`internal/provider/remote_magpie.go:69@e66d872`）。
- **其他**：保活（`internal/awake/awake.go:1-16@e66d872`）、Bark 推送（`internal/bark/bark.go:1-6@e66d872`）、Docker 镜像带 node/npm/uv 以运行 MCP server（`Dockerfile:1-41@e66d872`）。

**aigate 证据**

- 部署形态是「单应用实例 + PostgreSQL + TLS 反代」，应用在 loopback 明文监听，反代覆盖 `X-Real-IP`、关闭 SSE 缓冲与 POST 重试（`docs/architecture/README.md:1272-1282`；`deploy/nginx.conf:5-17`）。没有内置 TLS、mTLS 或证书管理，手册让部署者在 1Panel/OpenResty 上建 HTTPS 反代（`docs/runbooks/deployment.md:54`）。
- 上游网络策略是 aigate 的 netguard 等价物：校验 scheme/主机/端口/CIDR；拨号时重新解析并逐个校验地址，拒绝元数据地址（169.254/16、100.100.100.200/32、fd00:ec2::254/128）、链路本地、未指定、多播，私网与 loopback 需显式允许，另拒绝 0/8、100.64/10、198.18/15 等保留段（`internal/provider/network.go:43-47`、`internal/provider/network.go:107-132`、`internal/provider/network.go:135-150`）；有 DNS 重绑定与拨号时 SSRF 的专项测试（`internal/provider/network_test.go:47`、`internal/provider/network_test.go:81`、`internal/provider/network_test.go:115`）。aigate 没有抓取用户自选 URL 的场景，所以范围只到上游端点。
- 观测：`/metrics` 仅 loopback 可读，Prometheus 文本格式，四个计数（`internal/server/server.go:121`、`internal/server/server.go:647-659`）；无 OTLP。早期研究把「OTel 对外导出」列为后续（`docs/research/ecosystem-overseas.md:78`）。
- 来源证明：`third_party/provenance.json` 记录 New API/Sub2API 的固定提交、许可证与 NOTICE 的哈希（当前 `source_mode=reference_only`，无 vendored 文件）；`make check` 执行 `check-upstream-provenance`；Compose 镜像带 source-commit/tree/clean 三个标签（`compose.yaml:36-39`）。自有制品没有发布签名、构建证明或 SBOM：「真实生产部署、供应商认证和完整发布 SBOM 仍需独立验收」（`docs/architecture/README.md:98`）。
- Docker：多阶段构建，最终镜像 alpine、非 root（`Dockerfile:1-48`）；Compose 单机部署是「必须完成」项（`docs/design/phase-roadmap.md:111`）。
- 级联：041 规定「一个 outer attempt 最多经过一层 external relay……共同拒绝 aigate/New API/Sub2API 互连成链」（`specs/041-profit-hybrid-platform/spec.md:136`）。
- 通知：只有站内告警，且明确「不承担……外发通知」（`docs/architecture/README.md:1141`）。

**判定理由**：安装脚本与自更新是桌面应用分发形态，不适用；TLS 证书管理由反代承担是既定架构，不适用；egress 检测面向订阅区域合规，不适用。网络守卫在 aigate 已有的场景内已实现并验收，判已支持。发布信任链是部分支持。

### 2.10 遥测与隐私政策

**magpie 做什么**

- 每个运行中的正式版 magpie 每天向 PostHog 发一次事件：本机生成的随机安装 id、版本、系统与架构（`internal/stats/stats.go:1-12@e66d872`、`internal/stats/stats.go:71-104@e66d872`）。
- 除非用户关闭「共享我在用的 agent、供应商、模型和插件」，还会带：连接的 agent 的内部 id、开启的供应商（预设 id；自定义供应商只记 `custom`）、各 agent 设定的模型（厂商模型 id；models.dev 不认识的只记 `<provider>/other`）、各类数量与路由组数量（`docs/reference.md:4733-4746@e66d872`；`internal/stats/stats.go:100-113@e66d872`）。
- 合作伙伴（添加页与插件页里排在最前的赞助商）每天被展示、打开、添加的次数，以及插件页被打开的次数，只有计数（`internal/stats/stats.go:114-125@e66d872`；`docs/reference.md:4747-4754@e66d872`）。
- 另向 usemagpie.ai 报告 Discover 列出的插件中装了哪些，以及 Library 市场里在用的 MCP server 的市场 id（`internal/stats/plugins.go:22-30@e66d872`）。
- 不发名称、URL、账号、密钥、prompt、用量。总开关是设置里的「Count me as a user」，或 `DO_NOT_TRACK=1`、`MAGPIE_NO_STATS=1`；源码构建的版本从不发送（`internal/stats/stats.go:46-67@e66d872`；`docs/reference.md:4771-4776@e66d872`）。
- 与设置无关、服务器必然看到的：合作伙伴链接的点击与列表拉取（各带独立 id，不带安装 id 或地址）、图标代理（只转发图片，不计数不留存）（`docs/reference.md:4780-4798@e66d872`）。
- 顺带一提，magpie 还替用户关闭各 agent 自己的厂商遥测（`docs/subsystems/agent-wiring.md:18@e66d872`）。

**aigate 证据**：没有任何外发遥测代码；`internal/telemetry` 只是本机的结构化日志队列，记录类型仅四个字段（`internal/telemetry/telemetry.go:18-25`），并有测试断言记录模式只含固定元数据（`internal/telemetry/telemetry_test.go:149`）。用量与经营数据在 PostgreSQL，运营方通过经营报表查看（`docs/architecture/README.md:1366`）。

**判定理由**：厂商遥测只在「软件分发给别人运营」时才有意义；aigate 目前是运营方自持部署，产品分析需求已由数据库事实满足，不适用。「默认不记录正文」aigate 已实现并有测试，判已支持。

### 2.11 工程实践

见 §4。

### 2.12 多语言

**magpie 做什么**：界面默认英文，其他语言在 `internal/gui/assets/i18n.js`，键是 app.js 与 index.html 里的英文原句，缺键回退英文，`{name}` 占位（`internal/gui/assets/i18n.js:1-5@e66d872`）。该文件 22710 行，四个语言块：zh（第 6 行起）、zh-TW（5616）、ja（11210）、de（16806）。页面拆出去之后每个插件自带 `i18n.js`（library 5116 行、routing 3108 行、usage 1204 行、redact 536 行、sessions 444 行、otel 48 行）。语言选择跟随设置或系统语言，繁体中文按台湾/香港/澳门与 zh-Hant 判定（`internal/uilang/uilang.go:14-28@e66d872`）；托盘、通知与 TUI 也用同一套选择（`internal/uilang/uilang.go:1-5@e66d872`）。代码标准要求界面改动在 Chromium 与 WebKit 两个引擎、且中/英/繁中/日/德五种语言下跑界面测试，占位符必须一致（`docs/code-standards.md:91-105@e66d872`）。

**aigate 证据**：`<html lang="zh-CN">`（`web/index.html:2`）；Element Plus 固定加载 zh-cn（`web/src/main.ts:3`、`web/src/main.ts:11`）；日期格式化写死 `zh-CN`（`web/src/format.ts:33`）；后端错误文案直接是中文字符串（如 `internal/server/server.go:651`、`internal/server/gateway.go:77`、`internal/server/gateway.go:82`）。路线图把多语言列为后置：NA-16「品牌、多语言、外发通知后置」（`specs/038-new-api-capability-migration/capability-matrix.md:40`），024 的 F 切片「品牌/公告/语言与外发通知各自合同」（`specs/024-extension-platform/plan.md:36`）。

**判定理由**：未支持且已在路线图上后置。但 041 把 USD 付款定为商业首发必需，这与「只有中文界面」之间存在依赖冲突，值得重新排序，见 §3.1。

---

## 3. 值得评估清单

按建议优先级排列。P1 建议进入近期产品决策，P2 建议在对应阶段前评估，P3 暂记。

### 3.1 控制台、错误文案与对外文档的多语言（12.1）— P1

- **产品价值**：aigate 的控制台、API 错误文案、邮件模板、公开价格页全部只有中文。041 明确「USD/CNY 付款基准、USD/CNY 供应商成本及二者交叉组合是商业首发必需能力」（`specs/041-profit-hybrid-platform/design.md:81`）。收 USD 的客户若看不懂自助开户、充值、Key 管理界面，就必须靠人工代运营。
- **与商业目标的关系**：直接决定海外自助客户能否走完「注册→充值→建 Key→调用」闭环，是 041 商业首发的隐性前置。
- **实现复杂度**：中。前端 22 个视图（约 2443 行）与 38 个组件（约 2852 行），文案散落在模板、`ElMessage` 调用与表单校验里，需要引入 Vue i18n 并抽取字符串；后端错误需要从「中文 message」改为「稳定 code + 客户端文案映射」；邮件与公开披露文案还涉及法务一致性。
- **风险**：低。主要是范围控制——先做英文与 zh-CN 两种，占位符一致性加一条前端测试即可；不需要照搬 magpie 的五种语言与双引擎。

### 3.2 计量化的 Web Search 聚合与 web_fetch 工具（5.1、5.2，牵涉 5.3）— P1/P2

- **产品价值**：给没有原生搜索的模型提供服务端 `web_search`，对编码 agent 类客户端是常见需求。
- **与商业目标的关系**：这是天然适合「贡献毛利」模型的新商品——每次搜索有确定的按次采购成本，可加价出售，并能挂到 041 已有的 `tool_use` 任务类别上。
- **实现复杂度**：高。需要新的上游类型（搜索 API 供应商及其凭据池、按次计价的成本版本）；需要「服务端工具回合」状态机（模型发出工具调用→网关执行→回填→再次请求模型），它必须与现有不变量对齐：最多两次 outer attempt、写出字节后不切换、后台不重发；web_fetch 需要把现有上游网络策略扩展到任意 URL。
- **风险**：搜索厂商 API 的转售条款；返回第三方网页内容带来的内容责任；搜索加二次生成的延迟与超时；工具回合的预占与结算口径需要新冻结合同。

### 3.3 租户级出站脱敏（4.1、4.3、4.4）— P2

- **产品价值**：企业租户常要求「密钥与个人信息不离开平台」。规则层脱敏是成本较低的差异化能力。
- **与商业目标的关系**：041 的商品版本包含「隐私约束」，Key 已有模型/地域/来源等策略维度，脱敏可作为 Key 或租户级附加策略；对毛利无影响。
- **实现复杂度**：中。Go 规则层（正则、校验和、熵）可独立实现；稳定占位符需要每租户派生的 HMAC 密钥且只存内存；流式还原要为四种文本协议各自的 text 与 tool 参数路径包一层 writer。
- **风险**：
  - 计费口径：占位符会改变 token 数，客户计费应基于供应商实报 usage，需要冻结并在界面披露。
  - 可用性：需要模型处理该值本身的任务会失败，需要「永不遮蔽」名单。
  - 缓存：占位符稳定要求派生密钥不轮换，否则缓存失效。
  - 隐私：租户自定义词属于租户内容，存储与日志边界需明确；审计记录只存 kind、长度、首字符。
  - 必须沿用 magpie 的 fail closed：开启后遮蔽不可用则拒绝外发。
  - 模型层不建议引入：aigate 不应引入原生组件或 1.6GB 模型下载。

### 3.4 `/v1/models` 元数据增强与按客户端接入指南（1.10、1.11）— P1（低成本）

- **产品价值**：编码 agent 客户端依赖模型元数据决定行为（上下文窗口影响压缩时机，工具与推理等级影响选择器）。aigate 的 `/v1/models` 只有四个字段，客户端只能退回默认值。
- **与商业目标的关系**：影响开发者获客与兼容性，是「可售性」的一部分；与盈利路由无直接关系。
- **实现复杂度**：低。`inferenceModelsSQL` 可追加 `context_window`、`max_output_tokens`、能力标记（数据已在 `meter_model_profile` 中）；接入指南可由 Playground 现有的片段生成器扩展出按客户端的配置示例。
- **风险**：低。注意披露边界——041 要求动态商品公开候选范围、禁止未披露的替代（`specs/041-profit-hybrid-platform/spec.md:30`、`specs/041-profit-hybrid-platform/spec.md:82`），且 owner/member 不应看到内部 endpoint 与账号（`specs/041-profit-hybrid-platform/design.md:178`），模型元数据里不能带出上游端点或账号信息。Anthropic 形状的模型列表是否为 Claude Code 所需，本次未实测，见 §5。

### 3.5 发布制品签名、构建证明与 SBOM（9.3）— P2（第四阶段前）

- **产品价值**：aigate 以镜像与 Compose 交付给自托管客户或 1Panel 部署；没有签名与 SBOM 时，客户无法验证拿到的镜像对应哪个提交、是否被篡改。
- **与商业目标的关系**：架构正文已把「发行许可」「完整发布 SBOM」列为门禁（`docs/architecture/README.md:98`、`docs/architecture/README.md:1378`）。
- **实现复杂度**：低到中。现有 `third_party/provenance.json`、`check-upstream-provenance` 与镜像 source 标签是起点；补 cosign 镜像签名、`actions/attest-build-provenance` 与 syft SBOM 即可。
- **风险**：低。签名密钥可用 OIDC 联邦身份避免长期秘密。

### 3.6 "一键接入"链接或配置片段库（1.8）— P3

- **产品价值**：降低客户把 aigate 接入常用客户端的操作成本。
- **与商业目标的关系**：获客体验，间接。
- **实现复杂度**：低。但深链会与现有安全提示冲突——Key 创建对话框写明「不要放入浏览器前端、公共代码库或 URL」（`web/src/views/KeysView.vue:412`）。
- **风险**：中。自定义 scheme 链接里携带明文 Key 会进入浏览器历史、日志与剪贴板。建议只做「按客户端复制不含 Key 的配置片段」，不做带 Key 的深链。

### 3.7 mTLS 与机器身份（9.4）— P3

- **产品价值**：041 要求执行证据「绑定认证机器身份」后才可用于资格裁决（`specs/041-profit-hybrid-platform/spec.md:148`）。
- **与商业目标的关系**：外部执行器管理连接的身份基础。
- **实现复杂度**：中。通常由反向代理或服务网格承担，应用内实现 CA 与证书签发成本较高。
- **风险**：低；自行实现 CA 需要妥善保管与轮换。

### 3.8 OTLP 与可观测性导出（9.7）— P3

- **产品价值**：当前指标只有四个进程内计数，运营方难以做容量与延迟分析。
- **与商业目标的关系**：生产交付阶段的运维成熟度。
- **实现复杂度**：中。
- **风险**：低，但须坚持元数据 allowlist，不把正文、完整 URL、请求头附到 span（`docs/research/extended-operations.md:60`）。

### 3.9 备份自动化与加密封存（7.1、7.2）— P2（运维风险）

- **产品价值**：当前 RPO 完全取决于运营者手动执行；备份包含明文主密钥，安全性依赖存储。
- **与商业目标的关系**：商业平台的资金数据安全是底线。
- **实现复杂度**：低到中。加密封存可用成熟工具包装现有脚本；定时备份的难点在于现有脚本要求停流并持有维护锁，定时执行意味着定期停机，真正的在线备份需要 WAL 归档，属于 025 灾备范围。
- **风险**：低。注意密钥与备份分离保管，恢复后仍走双人核对。

### 3.10 外发通知（9.12）— 已规划，建议提前最小切片

- 路线图已把它放在 024 的 F 切片，并指出「021 身份邮件不能代替经营通知授权/投递认证」（`specs/024-extension-platform/plan.md:36`）。对运营中的商业平台，预算不足、渠道隔离、对账积压这类告警只显示在站内，意味着没人盯着就不会被处理。建议在 F 切片内优先做一种通道（webhook 或邮件），经 outbox 有界投递，失败不阻塞账本。

---

## 4. 工程实践借鉴

### 4.1 magpie 的三件套及其机制

**`AGENTS.md`（130 行）的写法**

- 它是一个指针文件加一份最难领域的判定表。五个小节：子系统设计与评审、代码标准、社区 PR、教训、已迁出到插件的内置订阅（`AGENTS.md:1-130@e66d872`）。前四节各只有两三句规则加一个链接。
- 最后一节对最容易误判的领域给出完整的状态判定：Moved/Not moved/Retired 三种情况下「实际运行的是哪份代码」，并直接写出排障步骤「这类订阅的缺陷报告通常出在插件上：1. 查是否已迁移 2. 在社区仓库修插件并发版 3. 提高迁移器的最低版本」（`AGENTS.md:51-118@e66d872`）。
- `CLAUDE.md` 只有两行：`@AGENTS.md` 与 `@LESSONS.md`（`CLAUDE.md:1-2@e66d872`），把教训文件也放进每次会话必然加载的上下文。

**`docs/code-standards.md`（267 行）的写法**

- 四张表：「一次变更包含什么」（11 条）、「测试」（12 条）、验收标准（6 条编号）、「评审一个 PR」（11 条），另加 Go、GUI、提交三小节（`docs/code-standards.md:8-22@e66d872`、`docs/code-standards.md:25-39@e66d872`、`docs/code-standards.md:41-125@e66d872`、`docs/code-standards.md:163-204@e66d872`、`docs/code-standards.md:206-267@e66d872`）。
- 每条规则都是「规则 + 评审者看什么 + 带日期的事故与提交号」。例如「失败的读取、解析或哈希意味着未知，绝不是空、无或相等」，后面列了四次因此放行的事故（`docs/code-standards.md:17@e66d872`）；「修复要覆盖缺陷的每一个兄弟」，后面是「2026-10-05 有六个修复因为只覆盖了报告里的情形而需要二次发布」（`docs/code-standards.md:15@e66d872`）。
- 验收标准第 1 条：「把旧代码放回去跑新测试，它必须以一个指名行为的真实 FAIL 失败；编译错误或被跳过的测试不算」（`docs/code-standards.md:47-50@e66d872`）。并要求「没运行的检查要列为未运行，不能省略」（`docs/code-standards.md:43-45@e66d872`）。
- 评审清单里的操作性条目：「跑起来——只读 diff 不算评审」「故意弄坏它」「核对供应商而不是 PR 的说法」「沿整条路径」（`docs/code-standards.md:168-188@e66d872`）。
- 提交规范：标题写「区域: 用户现在看到什么」，正文写如何验证、哪条测试在没有此变更时失败；一个提交一个目标；会话各自的 worktree 归各自所有，提交后核对 `git show --stat HEAD` 只列自己的文件（`docs/code-standards.md:220-267@e66d872`）。

**`LESSONS.md`（1916 行）的复盘机制**

- 文件首行是 `<!-- reviewed-through: e297a8ab2 (2026-10-13 23:30 +0800) -->`，标明复盘覆盖到哪个提交（`LESSONS.md:1@e66d872`）。
- 机制：「magpie 的代码由 agent 编写、评审、合并和发布。每晚复核当天的提交，找出不得不返工的、不该合并的、验证过薄的。每条教训先是一条规则，然后是为什么，然后是证据。 Seen 统计某次复核发现它的天数。同一条教训在 3 个以上不同日期被发现，就移入 code-standards」（`LESSONS.md:3-9@e66d872`）。
- 结构：按主题分节（读报告、修一类而非一个样本、用户自己的文件与设置、验证、并发与测试、红灯与发布、合并与关闭、已升格到代码标准），每条以「**规则**」开头，下面是若干条带日期、提交号或 issue 号的证据，末尾「Seen N× (日期…)」（例：`LESSONS.md:95-153@e66d872`、`LESSONS.md:158-173@e66d872`）。
- 还有「Done right on <日期>」条目，把做对的提交当作正面样例（`LESSONS.md:1869-1916@e66d872`）。
- 与代码标准的衔接：「已升格」一节保留规则与最新证据并回指 code-standards（`LESSONS.md:1278-1316@e66d872`）。

**子系统参考页**

- 每个子系统一页，固定包含职责与真相源表、运行路径、约束与失败行为、验证命令（例如 `docs/subsystems/mcp-router.md:54-66@e66d872` 给出精确到 `-run` 正则的测试命令）。
- 维护规则：改变文档化的职责、接口、状态转移、运行路径、兼容规则或失败行为时，同一个 PR 更新对应参考页；保持契约的内部重构不需要改文字（`docs/subsystems/README.md:28-34@e66d872`）。
- PR 语义描述：对每个受影响的子系统，用触发条件与前后行为说明可观察的变化；「这是作者提供的解释，由评审者对照 diff 与周边代码核实，不是自动证明的兼容性结论」（`docs/subsystems/README.md:36-42@e66d872`）。「仅更新文档不能证明实现；行为变更仍需既有测试与运行证据」（`docs/subsystems/README.md:51@e66d872`）。

### 4.2 aigate 现状对照

| 维度 | aigate 现状 | 与 magpie 的差距 |
| --- | --- | --- |
| `AGENTS.md` | main 27 行（`git show 41f66e4:AGENTS.md`）；工作树 41 行，新增「Agent skills」段（`AGENTS.md:29-41`）。强项是 SDD 优先与统一架构同步流程（`AGENTS.md:3-20`），以及五条实施与验证边界（`AGENTS.md:22-27`） | 没有非目标清单，没有排障路由，没有指向评审清单或教训文件的链接 |
| 贡献指南 | `CONTRIBUTING.md`（62 行）有按 SDD 修改的步骤、验证口径、专库要求、PR 要求；PR 模板四栏（`.github/pull_request_template.md`） | 与 `AGENTS.md:11-18` 近乎重复；没有评审操作清单 |
| 验证记录 | 每个切片一份 `docs/verification/*.md`，写明命令、结果与按层次的覆盖表（例：`docs/verification/key-runtime-limits.md:1-40`）；029 固定矩阵拒绝缺失、FAIL 与必需 SKIP | 记录了「跑了什么」，但没有「哪条新测试在旧代码上失败」 |
| 决策记录 | 5 份 ADR（`docs/decisions/ADR-001…005`），ADR-002 是「设计点到落地的细化」表 | ADR 是决策，不是缺陷复盘 |
| 复盘 | 散落在各验证记录里（VM 时钟回拨根因 `docs/verification/acceptance-workers-2-clock-analysis.md`、CI 超时根因 `docs/verification/maintenance-integration-isolation.md`、专库锁冲突 `docs/verification/key-runtime-limits.md`） | 没有跨切片、规则优先的汇总，也没有「改代码前先读」的要求；`docs/architecture/changes.md` 有 9074 行，是按时间的流水 |
| 提交信息 | 约定式标题，正文一到三句 | 正文普遍不写验证范围与「哪条测试必失败」（例：`7d60bb3` 只有标题，`50ddd93`、`40287bb` 两三句） |
| 测试 | 628 个 Go 测试文件对 633 个非测试文件；deterministic mock 与专用 PostgreSQL；测试名即合同（如 `internal/server` 中的 `TestGatewayPostgresAdmissionRechecksPermissionsAfterWaiting`） | 没有专门针对「新增枚举值必须登记」一类的守卫测试约定 |
| 并行协作 | 交付计划区分可并行与必须串行（`docs/design/delivery-plan.md:59-70`）；AGENTS.md 规定并行结果由集成人复核（`AGENTS.md:11-18`） | 没有 worktree 卫生规则（提交树应为 main 加本变更、核对 stat 只含自己的文件） |

### 4.3 对 aigate `AGENTS.md` 与开发流程的三条具体建议

**建议 1：在「实施与验证边界」里加两条硬条款——修复的兄弟面扫描，以及「旧代码上必须失败」的验证记录**

- **依据**：main 上 PR #35 在 18 分钟内落入 4 个同类鉴权修复（`gh api compare/41f66e4...main`，均为 2026-11-25 05:46–06:04 UTC）：`d501c23` 任务提交缺少归属校验（跨租户任务 ID 可触发供应商提交）→ `595d3de` 任务读取是租户级的 → `4553984` 取消任务不校验调用方、且 404 状态不一致 → `1df62a9` TTS 把空范围当作放行。我只读了提交说明和 `internal/server/server.go` 的一处补丁，没有逐个读 diff；但四条说明指向同一个缺陷类：**带资源 ID 的 Key 鉴权入口没有逐一校验资源归属与范围**。这正是 magpie 代码标准的第二条与教训里「一条限制要覆盖所有到达被保护物的路径」（`docs/code-standards.md:15@e66d872`；`LESSONS.md:167-173@e66d872`）所说的模式。本工作树的 `AGENTS.md:26` 目前只要求「先运行与变更相关的有意义测试」。
- **具体改法**：
  1. `AGENTS.md` 增加一句：「修复类变更须说明覆盖了哪些同形入口（其他协议、其他角色、其他资源类型的同类读写）；鉴权与范围类修复须附一张入口清单。」
  2. 验证记录或提交正文增加一行：「还原修复后失败的测试：<测试名> + 失败输出；本次运行/跳过/未运行的命令。」
  3. 评审步骤写明：「还原修复看测试变红；沿 HTTP→Reserve→Attempt→Finalization→Settle→报表追一遍调用方。」
  4. 配一个守卫测试：枚举所有 `/v1/*` 带资源 ID 的路由，断言每条都登记了归属校验谓词，新增路由未登记即失败（对应 magpie 的 `TestEveryRouteIsNamedForTheCLI` 一类守卫，`docs/code-standards.md:77-82@e66d872`）。

**建议 2：建立规则化的复盘文件，并挂进 `AGENTS.md`**

- **依据**：aigate 已经有高质量的事故分析，但它们按切片存放，下一个会话改代码时不会读到。例如「VM realtime 多次回拨约 1 秒，能解释重试未到期、审批顺序颠倒」（`docs/verification/acceptance-workers-2.md`）、首轮运行器专项与浏览器 QA 争用专库实例锁（`docs/verification/key-runtime-limits.md`）、本地 refresh 把 `.claude/settings.local.json` 纳入清单导致干净 CI 检出失败（`docs/architecture/changes.md:8344`）。magpie 的教训文件里恰好有对应的三条规则：时序测试两侧留余量、同一台机器一次只跑一套完整套件、一条红灯的 issue 要读到最后一条评论（`docs/code-standards.md:32@e66d872`、`docs/code-standards.md:37@e66d872`、`docs/code-standards.md:39@e66d872`）。
- **具体改法**：
  1. 新建 `docs/lessons.md`，首行 `reviewed-through: <sha>`；每条固定为「规则 → 为什么 → 证据（提交或验证记录链接）→ 出现日期」。
  2. `AGENTS.md` 加一行：「改代码前先读 `docs/lessons.md`。」
  3. 同一规则在 3 个以上不同日期出现，就升格为 `AGENTS.md`「实施与验证边界」的一条。
  4. 首批种子可直接取自现有记录：测试机时钟先证明单调再解释超时；占用同一专库实例锁的测试必须串行；架构清单不得纳入本地会话文件；鉴权修复要覆盖同一资源的全部入口（建议 1）。
  5. 每次集成人做架构 reviewed refresh 时，顺带把当期验证记录里的「首次失败与根因」抽成教训条目。

**建议 3：修正 `AGENTS.md` 的失效指针，并补「非目标」与「排障路由」两小节**

- **依据**：
  - `AGENTS.md:41` 写「单上下文布局：根 `GLOSSARY.md` + `docs/adr/`」，但仓库没有 `GLOSSARY.md`，也没有 `docs/adr/`；ADR 实际在 `docs/decisions/`（ADR-001 至 ADR-005）。`docs/agents/domain.md` 同样指向 `docs/adr/`（`docs/agents/domain.md:9`、`docs/agents/domain.md:22`）。
  - 非目标散在四处：`specs/038-new-api-capability-migration/spec.md:198-204`、`specs/041-profit-hybrid-platform/spec.md:27-34`、`docs/design/phase-roadmap.md:120-122` 与 `docs/design/phase-roadmap.md:162`。agent 需要读完三四份文档才能确认「某能力是否该做」。
  - `AGENTS.md` 没有告诉 agent「遇到某类现象先读哪份规格、查哪张表」。magpie 在这一点上给了明确范例（`AGENTS.md:107-118@e66d872`）。
- **具体改法**：
  1. 把 `docs/adr/` 改为 `docs/decisions/`（两处），下一次 reviewed refresh 同步架构清单。
  2. 加 6 行「非目标」：任意插件/脚本/MCP 执行、签到/邀请/分佣、导入或双写下游客户与钱包数据、多实例 HA、新增客户侧专有协议、媒体动态路由——每条后附规格出处。
  3. 加 6–8 行「排障路由」：请求失败或计费不符 → `X-Request-ID` 关联 request/attempt/finalization/settlement；为何选了这个渠道 → 033 只读回放（`cmd/moni-aigate/route_replay.go`）；CAS 409 或结果未知 → 只读核查后显式新编辑；告警 → 024-A；备份恢复 → 对应 runbook。
  4. 把 `AGENTS.md:11-18` 与 `CONTRIBUTING.md:30-40` 的重复流程保留一处，`AGENTS.md` 只留指针。

### 4.4 可借鉴但不改动现行范围的做法

- **代码标准表**：以 `docs/code-standards.md` 的形式把 aigate 已有的不变量做成评审清单，并补上几条与本项目高度相关的：另一应用（供应商、New API、Sub2API）的值取自它自己的源码或实测，不凭字段名猜；供应商规则只作用于该供应商的 profile；平台默认值不覆盖用户已设的值；未知不等于 0 或成功。
- **每个能力域一节验证命令**：aigate 的命令散在 `docs/runbooks/development.md` 各节与 `docs/verification/*.md`。可在统一架构的能力表（`docs/architecture/README.md:1358-1379`）增加一列「验证命令与记录」。
- **架构入口页先图后史**：`docs/architecture/README.md` 前 115 行是按日期排列的流水段落（随后是上下文图），「系统上下文与边界」从第 127 行才开始。可把流水移到 `changes.md`，让入口页保持「先上下文、模块、请求链路，再能力表」。
- **扩展点各自声明失败策略**：magpie 把中间件设为 fail open、把遮蔽与限额设为 fail closed，并写在同一个页面上（`docs/subsystems/plugin-runtime.md:236-240@e66d872`、`docs/subsystems/plugin-runtime.md:267@e66d872`）。aigate 不做插件系统，但 032/033 的过滤器与 041 的候选门禁同样值得在规格里逐点写明失败策略。
- **「只回收自己写的内容」的所有权记录**：aigate 不写用户文件，但运维脚本与未来 041 的 DownstreamAdminConnector 对下游资源做「窄类型写」时会面对同一个问题——需要记录自己写入的内容摘要，撤回时只动仍与自己写入一致的资源（041 设计里的 desired/observed digest 与 drift 状态已朝这个方向走，`specs/041-profit-hybrid-platform/design.md:106-122`）。
- **提交与 worktree 卫生**：一个提交一个目标；提交后核对 `--stat` 只含自己的文件；提交树应为 main 加本变更（`docs/code-standards.md:239-267@e66d872`）。aigate 多会话并行，本工作树即落后 main 5 个提交，这条值得写进并行小节。

### 4.5 不建议照搬

- 每日上百次提交与发布、每晚 agent 复盘的节奏——aigate 是商业账务系统，需要的是与切片节奏匹配的复盘，而不是按日。
- 社区 PR 的作者观察与封禁名单机制——aigate 当前没有社区 PR 面（`docs/agents/issue-tracker.md:32`）。
- 双浏览器引擎、五语言的界面测试矩阵——先做两种语言一种引擎。
- 文档里的私有工具名与机器名（如 `mote-run`、`mica`）——对 aigate 无意义。

---

## 5. 不确定点

1. **容器运行验收的表述不一致**：`docs/architecture/README.md:43` 写「Compose 实际覆盖迁移、provision/bootstrap……以及 app/worker/PostgreSQL 重启」；同文 `docs/architecture/README.md:1378` 写「实际容器未验」。前者是 2026-11-10 的集成复核，后者指 025-A0 切片。9.8 判「已支持（待核实）」，需由维护者确认当前结论。
2. **`/v1/models` 对编码 agent 客户端的实际兼容性未实测**：aigate 只有 OpenAI list 形状，且仅四个字段；Claude Code、Codex 等客户端是否依赖 Anthropic 形状的模型列表或 `context_window` 等元数据，本次没有运行验证。
3. **magpie 的 agent 数量口径**：README 列 90 个名字；代码字面量 93 个构造器；差三个是按名称对照得出的（DS Bot、Obsidian、Antigravity 桌面应用）。没有逐一验证每个适配器的实际可用性；WSL 孪生与插件 agent 另计。
4. **社区插件未核**：`plugins/` 目录只含 15 个内置插件包；社区插件仓库（magpie-community/plugins）不在本地 checkout，其数量、质量与条款风险未评估。AGENTS.md 自述有 12 个订阅已迁出到社区插件，其中 Antigravity 插件因封号风险对新用户撤回（`AGENTS.md:25-98@e66d872`）。
5. **magpie 遥测与签名的实际行为未运行验证**：上报内容以 `internal/stats/stats.go` 与 `docs/reference.md:4724-4798@e66d872` 为准，PostHog 端如何处理未核；构建证明「取不到则放行、存在但失败则拒绝」的语义以 `internal/update/attest.go:27-60@e66d872` 的注释为准。
6. **「aigate 作为 MCP 服务端」的归属**：规格的用词是「MCP 执行」「Agent 工作流」，没有逐字覆盖「aigate 自己对外提供 MCP 端点」。5.3 判未支持并标为待产品决策。
7. **记忆类能力的判定依据**：判不适用的依据是形态（读本机 agent 会话、注入 agent instructions）加上 041 非目标的精神，而非逐字条款。若产品方考虑「租户级记忆产品」，那是独立的产品决策，涉及客户内容存储与额外 LLM 调用的计量。
9. **`LESSONS.md` 未逐条读完**：1916 行、112KB，本次读了机制说明（`LESSONS.md:1-91@e66d872`）、部分条目（`LESSONS.md:93-200@e66d872`）与「已升格」一节（`LESSONS.md:1278-1330@e66d872`、`LESSONS.md:1880-1916@e66d872`）；4.1 对其格式的概括以这些为据。
10. **未读的 magpie 文档**：`gateway-routing.md`、`gateway-sessions.md`、`gui-shell.md`、`claude-subscription-bridge.md`、`provider-plugins.md`、`providers-accounts.md`、`gateway-request-hooks.md` 不在必读清单内，与本主题关系较小；其中若有与 Agent 生态交叉的内容（例如 gateway-sessions 对 agent 会话文件的读取）本稿未覆盖。
11. **PR #35 四连修的观察依据**：只读了四条提交说明与一处补丁，未逐个读 diff；「同一缺陷类」的判断来自说明文字。
12. **aigate 基线**：证据取自工作树 `ded2312`；main `9fd8dd6` 的 5 个新提交已核对不涉及本审计范围，但未在 main 上重新逐行确认每一处引用。
