# magpie Web GUI 界面 / 信息架构 / 交互研究（供 moni-aigate 控制台参考）

- 对象：magpie `e66d872a8eaecc285b0d597917d6b520035712ec`（2026-11-25），只读源码与仓库内截图，**未运行**。
- 对照：moni-aigate worktree `eloquent-faraday-47604e`（HEAD `ded2312`）的 `web/src/`，同样只读、未运行。
- 引用约定：magpie 路径相对 `/tmp/magpie-study`，前缀 `internal/gui/assets/` 省略为 `assets/`，行号后缀 `@e66d872`（下文所有 magpie 源码/文档行引用均带此后缀；截图为同一提交下的仓库文件，不带行号）；aigate 路径相对 worktree 根。
- 代码规模提示：`assets/app.js` 24,960 行（1.35 MB）、`assets/plugins/routing/client.js` 10,143 行、`assets/plugins/usage/page.js` 6,024 行、`assets/plugins/library/client.js` 10,600 行、`assets/i18n.js` 2.66 MB。本报告只精读了与题目相关的函数（估计不到 15%），未读部分在第 7 节列明。

---

## 1. 页面清单与导航结构

### 1.1 导航如何组织

| 事实 | 证据 |
| --- | --- |
| 一级导航分四组：**Work / Models / Insights / Extend**，外加页脚的 Settings | `assets/index.html:73-89@e66d872`（Work：Agents、Sessions、Library、Artifacts；Models：Providers、Routing、Routing groups、Gateway；Insights：Usage、Masking；Extend：Plugins） |
| 窗口宽 ≥1024px 时导航移入左侧栏（侧栏列出当前页自己的子标签）；更窄回到顶部分段标签；用户可强制选侧栏或顶栏（`magpie.navLayout`） | `assets/app.js:24010@e66d872`（`SIDE_AT`）、`assets/app.js:24020-24187@e66d872`（`layoutNav`）、`docs/subsystems/gui-shell.md:22@e66d872` |
| 侧栏页可**隐藏、可拖拽排序**（"Sidebar pages" 设置，`magpie.navHidden` / `magpie.navOrder`）；Providers 不可隐藏 | `assets/navpages.js:31-109@e66d872`、`assets/navorder.js:8-31@e66d872` |
| 页面由"壳 + 插件槽"组成：内置插件（Gateway / Sessions / Library / Routing / Usage / Redact / OTel）各自注册 page slot 与自己的 HTML | `assets/plugins/routing/client.js:19-49@e66d872`、`assets/plugins/sessions/client.js:2142-2149@e66d872`、`assets/plugins/usage/client.js:15-157@e66d872`、`assets/plugins/gateway/client.js:890@e66d872`、`assets/plugins/library/client.js:10549-10551@e66d872` |
| 右上角常驻：刷新（按当前页决定刷新什么）、置顶、**⌘K 全局设置搜索**、设置 | `assets/index.html:100-116@e66d872`、`assets/app.js:23787-23816@e66d872` |
| 托盘面板是独立的 4 标签紧凑版：Agents / Allowances / Usage / Routing | `assets/index.html:131-136@e66d872`、截图 `site/public/img/panel-light.png` |
| 手机（`magpie web`）：页面变成**底部 Tab 栏**，最多 5 个，多出的进 "More" | `assets/phonetabs.js:18-52@e66d872`、`assets/app.css:393-429@e66d872` |

**四组的语义是"任务阶段"**：Work = 用模型干活；Models = 配置模型来源与路由；Insights = 看用量与隐私；Extend = 扩展。它把"配置"和"观察"分开，Agents 页只做一件事——给每个 Agent 选模型。

### 1.2 页面清单（核心任务与标签）

| 组 | 页面 | 核心任务 | 内部标签 / 分区 | 主要证据 |
| --- | --- | --- | --- | --- |
| Work | **Agents** | 给本机每个 Agent CLI 选模型 / 推理档位，接入或断开 magpie | 列表 + 展开的 Details（版本、模型多选、配置 diff）；底部 Profiles（方案） | `assets/app.js:492-875@e66d872`、截图 `agents-zh-light.png` |
| Work | **Sessions** | 按项目文件夹浏览所有 Agent 的历史会话，恢复、导出、删除 | Agent 标签、文件夹折叠、会话行、回收站 | `assets/plugins/sessions/client.js:654-741, 1142-1398@e66d872` |
| Work | **Library** | 指令、MCP、技能、子代理等"写一次、分发到每个 Agent" | Instructions / MCP servers / Skills / Hooks / Environment variables / Subagents / Slash commands / Rules / Claude plugins | `assets/plugins/library/client.js:866, 1017-1027@e66d872` |
| Work | **Artifacts** | 分享的产物 | — | `assets/index.html:78@e66d872` |
| Models | **Providers** | 添加供应商 / 订阅，管理 key 与账号 | 列表、添加面板（分区瓦片）、编辑器对话框 | `assets/app.js:6270-6437, 7274-7457, 9551+@e66d872`、截图 `providers-light.png`、`add-zh-light.png` |
| Models | **Routing** | 实时观看每个请求被路由到哪个账号以及**为什么** | 舞台、故事、上下文卡、请求列表、Accounts and keys、按天回放 | `assets/plugins/routing/client.js:19-345@e66d872`、截图 `routing-zh-light.png` |
| Models | **Routing groups** | 创建/编辑路由组（成员、模式、规则、模板） | 工具栏、模板卡、组卡、组设置、多 key 供应商池 | `assets/plugins/routing/client.js:6420-6503, 6976-7100, 7567-7637, 7836+@e66d872`、截图 `intent-rules-zh-light.png` |
| Models | **Gateway** | 网关状态、**Caller keys**、连接代码片段、模型列表、最近调用 | Connect（可折叠）/ Models / Recent calls / Gateway keys | `assets/plugins/gateway/client.js:890-930@e66d872`、`assets/plugins/gateway/keys.js:160-257@e66d872` |
| Insights | **Usage** | 额度、花费、请求账单、会话统计、上下文 | Overview / Requests / Sessions / Context | `assets/plugins/usage/client.js:19-142@e66d872`、截图 `usage-zh-light.png`、`usage-ja-dark.png` |
| Insights | **Masking** | 脱敏观测：多少值没让厂商看到 | 汇总卡、按请求/按值列表、试一试 | 截图 `masking-zh-light.png` |
| Extend | **Plugins** | 内置插件开关、已安装、市场 | Built-in / Installed / Discover | `assets/plugins.js:1-7@e66d872` |
| 页脚 | **Settings** | 通用、额度显示、网络共享、模型、隐私、可观测性、同步备份、关于 | 8 个可见标签 + 隐藏的 Plugins 标签 | `assets/index.html:179-513@e66d872`、`docs/screenshots/font-settings.png` |

页面总数：**11 个一级页面 + Settings（9 个标签）+ 插件自带页**；若把标签级视图都算上是约 32 个可寻址视图（Usage 4、Library 9、Plugins 3、Settings 9 等）。另外还有托盘面板 4 标签。

### 1.3 让"多账号 / 多模型 / 多路由"可理解的 8 个设计

1. **统一标识贯穿全站**：一切模型都写成 `provider/model`，路由组写成 `group/<id>`，在 Agents 的 picker、Providers、Routing、Usage 里完全一致（`assets/plugins/routing/client.js:7567-7571@e66d872`）。
2. **"当前用的模式"既是标签也是入口**：舞台上的模式 chip、组卡上的模式 tag 都可点击，直接打开模式菜单或跳到该组的定义处（`assets/plugins/routing/client.js:3184-3236@e66d872` 的 `goTo/goRouting`、`assets/plugins/routing/client.js:7676-7707@e66d872` 的 `routeTag`）。
3. **每个数字都有一句话解释算法**：例如 Pace = 已用比例 − 已过时间比例（`assets/app.js:14926-14953@e66d872`）；某种路由为什么把某账号排第一，由 `firstWhy` 逐条生成人话（`assets/plugins/routing/client.js:1019-1175@e66d872`）。
4. **复杂度按数量自动折叠**：≥3 个同供应商 key/账号折成一行 fold（`FOLD_AT = 3`，账号 ≥6 才默认折叠 `LONG = 6`）；Accounts and keys 只先画 8 行（`ACTS_SHOWN = 8`）其余进 "Show N more"（`assets/plugins/routing/client.js:1954-1957@e66d872`、`docs/subsystems/gui-shell.md:265-317, 369-412@e66d872`）。
5. **"已用 / 剩余"是全站唯一开关**（`quotaLeft`）：Providers 编辑器、Routing 账号行、Usage 卡片、托盘菜单栏四处同源同形（`assets/app.js:14898-14924@e66d872`）。
6. **空态画出"填满后的形状"**：Routing 没请求时仍画出"your agent → magpie → 账号列表"的舞台骨架并说明会发生什么（`assets/plugins/routing/client.js:6136-6170@e66d872`）；新用户的 Agents 页有三步"Get started"卡，打勾来自真实状态（`assets/app.js:1764-1930@e66d872`）。
7. **用户可以裁剪界面**：隐藏页面、排序页面、隐藏某张额度卡或某行窗口（`settings.UsageCardsHidden / UsageWindowsHidden`）、隐藏小而闲置的客户端（`assets/plugins/usage/page.js:1043-1057@e66d872`）。
8. **跨页互通且带返回**：Usage 的 "Settings ›" 打开设置并高亮那一行、记住来处，页头出现返回按钮（`assets/app.js:23757-23805@e66d872`）。

---

## 2. 逐页研究

每页按：布局 → 关键组件 → 信息密度 → 状态表达 → 空/错/加载态。

### 2.1 Agents 页

- **布局**：一张列表卡，每行 = Agent 图标 + 名称 + 一行状态 + 右侧"宽列（模型）+ 窄列（effort 等）+ 方形小按钮" + 开关；已接入的行点击任意处展开 Details（`assets/app.js:512-760@e66d872`）。页底是 Profiles 芯片条（"Budget""Focus" 一键切换所有 Agent 的模型，`assets/index.html:140-144@e66d872`，截图 `agents-zh-light.png`）。
- **信息密度**：低。默认每行只有"名称 + 一句状态 + 两个控件 + 开关"，其余 21 个未设置的 Agent 折进"显示其余 N 个"。
- **模型选择器（picker）**（`assets/app.js:4870-4971, 5323-5355, 5526-5594@e66d872`，截图 `picker-light.png`）：
  - 顶部搜索框文案就是承诺："Filter, or type any model id…"——既可过滤也可**直接输入任意模型 id**；
  - 左侧**供应商导轨**（图标列）：全部 / 收藏 / 路由组 / 各供应商，点击即筛选；
  - 列表按供应商分组（Routing groups 在最前），当前模型置顶；每行 = 图标 + 名称 + 灰色来源（"Kimi · via magpie"）+ 徽标（FREE、0.5× 倍率、上下文长度、`42 tok/s` 或首 token 秒数、繁忙度）+ 收藏星 + 勾；
  - "Default / what Codex ships with" 作为第一项，并用一句话说明默认值含义（`assets/app.js:4919-4937@e66d872`）；
  - 找不到的用户的路由组也列出来并写明为什么不能选（`unservedRows`，`assets/app.js:4866@e66d872`）；
  - 触屏上打开不自动聚焦搜索框，避免键盘挡住列表（`assets/app.js:4969-4971@e66d872`）。
- **状态表达**：行首小圆点（已接入=绿）+ 文字"已接入 · 在 /model 里切换 opus · sonnet · haiku 档位"；失败态的点画成 **×** 而不是红点（色盲可辨，`assets/app.css:42, 1894@e66d872`，`docs/subsystems/gui-shell.md:817@e66d872`）。
- **加载态**：5 行骨架（图标 + 名称 + 两个控件位）（`assets/app.js:469-490@e66d872`）。**空态**："No agents found" + 安装提示（`assets/app.js:505-508@e66d872`）。
- **Details 首次展开**：先读该 Agent 自己的设置再画，期间显示 Loading 且禁用，失败可重试（`docs/subsystems/gui-shell.md:45-56@e66d872`）。

### 2.2 Providers 页

- **布局**：搜索框 + Select（批量删除）+ "Hide accounts"（打码）+ 一张列表；已停用的供应商折叠到下方"Turned off"（`assets/index.html:158-167@e66d872`）。
- **行**：logo（也是拖拽把手，顺序 = 尝试优先级）+ 名称 + 徽标（sponsored / 已停用 / This magpie / 代理缺失 / 当前费率 ×N / 计费类型）+ 副行（"signed in as {user} · 3 models"）+ **正在使用它的 Agent 图标**（点击即改）+ key/套餐药丸 + 开关（`assets/app.js:6295-6385@e66d872`，截图 `providers-light.png`）。
- **搜索**覆盖名称、id、预设、地址、每个模型 id、账号、套餐、key 名；搜索中仍可拖动排序（`assets/app.js:6478@e66d872`，`docs/subsystems/gui-shell.md:840@e66d872`）。
- **添加面板**（`assets/app.js:7274-7457@e66d872`，截图 `add-zh-light.png`）：搜索框 "Find a vendor, or paste a URL and key…"（可直接粘贴 curl 命令/环境变量生成自定义供应商），下面按语义分区铺瓦片：**订阅 · 登录即可，无需密钥** / 供应商 / 中转 · 一个密钥背后多家供应商 / 本机 / 自定义；已添加的变灰打勾；无结果时给两条出路（"Add it as a custom provider" / "look for a plugin"）。
- **编辑器**是对话框（600px，可到 880px；`docs/subsystems/gui-shell.md:847@e66d872`）：头部有官网链接与开关；顶部"Agents"芯片显示谁在用它；名称 / ID / 协议分段 / URL / key / 模型多选 / **账号列表** / 路由方式 / 高级折叠（`assets/app.js:9551-9720@e66d872`）。
- **账号行**（`assets/app.js:13753-14086@e66d872`）：勾选点 + 账号名 + 套餐芯片 + 权重 + 自有模型 / 用量上限 / 并发 药丸 + 状态词（In use / First / Paused / Signed in / **Sign-in required** / Access expired）+ 动作（Use / Make first / Sign in again / Remove）。未设置的药丸在悬停前隐藏，避免一排空药丸。
- **配额窗口**：账号 hover 显示"5 hours 40% used · resets in 2h"；Usage 卡片同源（`docs/subsystems/gui-shell.md:319-338@e66d872`）。
- **加载态** 5 行骨架（`assets/app.js:6230-6247@e66d872`）；批量删除用自有确认框列出全部名称（`assets/app.js:7771@e66d872`）。

### 2.3 Routing 页（实时 trace）与 Routing groups 页

**Routing 页布局**（`assets/plugins/routing/client.js:78-165, 172-345@e66d872`，截图 `routing-zh-light.png`、`intent-trace-zh-light.png`）：

1. 顶部：`<组名> · 路由组 · 启用 3 · 4 个 Agent 同时在发` + **一段人话解释当前模式**（"智能：在还有余量的账号里，额度最先重置的排第一……"）+ "Expand" 展开深度选择。
2. **舞台**：左列 Agent 节点、中间 magpie 网关节点、右列账号/模型候选行；SVG 连线，请求进行时有"喜鹊"沿线飞行与流动虚线；正在回答的行高亮描边，限流的行虚线框 + "限流 · 6 秒后恢复"。
3. 页脚：一句话结论（"DeepSeek 排第一。"）+ 三个数字（请求 / 改道 / Agent 收到的错误）。
4. **故事（story）**：有序列表逐句说明（`assets/plugins/routing/client.js:3826-3926@e66d872`，样式 `assets/plugins/routing/routing.css:255-267@e66d872`）：
   - 实心点 = 主句（"OpenCode 请求了路由组 Long context: a, b, c"）；
   - 加粗深色 = 决策原因（"第 1 轮开始，命中规则 3（意图是"writing or fixing tests"），所以 … 排第一"）；
   - 空心点 = 旁注（"这个会话上次由 OpenRouter 回答，但当时厂商只从缓存读了 0 tokens——不值得为此留下"）；
   - 绿点 / × 点 = 每次尝试成功或失败；失败时附"它说：{厂商原话}"；
   - 每次尝试还给出**时间去向**（掩码 / 排队 / 首 token / 传输）。
5. **上下文窗口卡**（见 2.6）就放在故事下面。
6. 下方两栏：左 = 请求列表（按天胶囊、By request / By session / By key、用途多选、指标多选、CSV 导出），右 = **Accounts and keys**（每个账号一格：点、名称、套餐、Try now、剩余额度、当日尝试/成功/失败计数；只画 8 行，其余 "Show N more"）。

**Routing groups 页布局**（`assets/plugins/routing/client.js:6420-6503, 6976-7100@e66d872`）：

- 工具栏：`‹ Routing` 返回、过滤框、"N groups · M on"、Hide plans、Templates、Import、Select、**New group**（唯一实心按钮）。
- 没有自建组时先展示**模板卡**（"Hard to strong, easy to cheap" / "Cheap first" / "Never stuck" / "Plan first" / "Fastest first" / "Spread evenly"），每张卡写明由用户自己的哪些模型组成，带 Add / Edit first，缺条件时写明缺什么（`assets/plugins/routing/client.js:7283-7349@e66d872`）。
- **组卡**（`assets/plugins/routing/client.js:7567-7637@e66d872`，截图 `routing-zh-light.png` 底部）：叠放图标（拖拽把手）+ 名称 + `group/daily-coding` 等宽小字 + 成员行（顺序模式用 `→`，其余用逗号）+ 每个 provider 的账号合计额度行 + 模式 chip（可点开菜单直接改）+ 其他 tag（Session / Rate limited to the back / 1 rule / N switched off / no member ready）+ Edit + 开关。
- **组编辑器**（`assets/plugins/routing/client.js:7836+, 8016-8260@e66d872`，截图 `intent-rules-zh-light.png`）：名称 → 成员列表（每行：开关、把手、图标、名称·供应商·账号、模型信息芯片、推理档位 chip、速度 chip、账号 chip、权重、Up / Down / Remove）→ 路由分段（智能 / 按顺序 / 轮流 / 最少使用 …）+ **一句话说明** → 会话保持分段 + 说明 → 规则行（"当 ≥ 128000 tokens / 带图片 / 不限推理 / 意图是 … → 先发给 {模型}"，按顺序自上而下检查）→ 意图判断模型 → 底部 Remove / 取消 / 保存。
- 模式说明文案集中维护（`MODES` `assets/plugins/routing/client.js:784-808@e66d872`、`GROUP_HINT` `assets/plugins/routing/client.js:6484-6503@e66d872`），每个模式一段 1–3 句的解释。
- **空/错态**：舞台空态见 1.3；网关不在此进程时显示"谁在路由"（`assets/plugins/routing/client.js:6297-6308@e66d872`）。

### 2.4 Usage 页

页头（`assets/plugins/usage/client.js:19-31@e66d872`）：标签分段（Overview / Requests / Sessions / Context）+ 周期分段（今天 / 7 天 / 30 天 / 全部）+ 刷新与自动刷新间隔 + **右上角总花费**。

**Overview**（`assets/plugins/usage/client.js:33-61@e66d872`、`assets/plugins/usage/page.js:1069-1343@e66d872`，截图 `usage-zh-light.png`、`usage-ja-dark.png`）：

- **额度卡**（Allowances）：每个订阅/账号一张卡，内含多个窗口 meter（名称 + "62% used" 可点击切换已用/剩余 + 4px 进度条 + 重置行 "Resets 14:26" + **Pace +12%**）+ 剩余额度曲线（对匀速消耗线）+ 余额行 + "Updated 3 min ago"（`assets/app.js:16457-16678, 19012-19060, 14926-14953@e66d872`）。
- **卡片 ⇄ 表格**：账号多时切成表格，一行一个账号，列 = 订阅 / 账号 / 5 小时 / 1 天 / 7 天 / 月 / 其他，每格是"百分比 + 细条 + 重置时刻"，点表头三态排序（已用最多 → 反向 → 原顺序），多账号供应商聚合成一行均值可展开（`assets/quotatable.js:80-99, 101-274@e66d872`）。
- **"All N accounts" 条**：同供应商多账号合并读数（数值窗口相加、比例窗口取同套餐均值、读失败记为"1 couldn't be read"而不是 0）（`docs/subsystems/gui-shell.md:414-454@e66d872`）。
- **KPI 四格**：Token（输入 21M · 输出 2.2M）/ 缓存读取（命中率 88%）/ 推理 / 次调用（失败 N · 重试成功 N）；Token 标签是下拉，可切"含缓存/不含缓存"（`assets/plugins/usage/page.js:941-969@e66d872`）。
- **柱状图**：输入在下、输出在上叠放，每根柱 tooltip 给 token 数与花费（`assets/plugins/usage/page.js:1126-1149@e66d872`）。
- **四个排行列表**：Agents / Models / Accounts / Gateway keys，行 = 图标 + 名称 + 副行（N calls · TTFT · 速度 · 失败 · 重试）+ **份额条** + 大数字（23M）+ 小字（in · out · cached · hit rate）+ 花费；标题可折叠；小而闲置的客户端自动折进 "Other"（`assets/plugins/usage/page.js:1043-1057@e66d872`）。
- **诚实数字**：总额旁标"≈$76.59 标价"，有未定价调用时改成"≈$48.19+ N calls unpriced"，绝不让缺失价格读成 0（`assets/plugins/usage/page.js:615-628@e66d872`）。

**Requests**（`assets/plugins/usage/client.js:62-115@e66d872`）：筛选器芯片（电脑 / Agent / 用途 / 供应商 / 模型 / 账号 / key / 状态）+ 搜索 + 导出 CSV；KPI 条；趋势图（指标 / 样式 / Split by）；**Model speed 表**（按模型或供应商：tok/s、首 token、失败率、换模型率、正确率）；**53 周热力图**；账单表列 = 时间 / Agent / 请求的 / 供应商·账号 / 实际发送 / 实际服务 / 档位 / In / Out / 缓存写 / 缓存读 / 命中率 / 成本 / TTFT / 时长 / 速度（`assets/plugins/usage/page.js:3424-3426@e66d872`）。点一行进入**该请求自己的页面**（"Requests ‹" 返回原位置），列出全部字段，Routing 只是其中一个"View routing"链接（`assets/plugins/usage/page.js:2173-2370@e66d872`）。

**Sessions**（`assets/plugins/usage/client.js:117-142@e66d872`、`assets/plugins/usage/page.js:4643-4836@e66d872`）：Agent / 模型 / 文件夹三个下拉（选项后带"N tokens"）；KPI；日历图；卡片网格（Projects / Models / Hours / Top / Shape / Skills / Tools）；最近会话列表。

**Context**：见 2.6。

**加载态**：4 个 KPI 骨架 + 2 张卡骨架，已画出的卡保留不动（`assets/plugins/usage/page.js:581-613@e66d872`）；静默刷新（刷新时图标转但卡片不消失）。**空态**："No calls in {period}" 并说明统计口径。

### 2.5 Sessions 页

- **布局**（`assets/plugins/sessions/client.js:654-741@e66d872`）：Agent 分段标签（图标 + 名称 + 数量）+ 一个显示当前 Agent 的下拉按钮（`assets/plugins/sessions/sessions.css:3-13@e66d872`）+ 搜索框 + Select + Trash + "Settings ›"。
- **文件夹行**（`assets/plugins/sessions/client.js:1142-1231@e66d872`）：折叠箭头 + 文件夹名 + 上级路径（home 显示为 `~`）+ 最新会话标题 + "3 天前" + "12 sessions" + ⋯（Relocate project / Delete all sessions）。临时目录、根目录、隐藏目录折进"Other folders"。
- **会话行**（`assets/plugins/sessions/client.js:1280-1398@e66d872`）：标题 + 副行（时间 · 模型 · N messages · 大小 · id 前 8 位）+ 花费 + **速度格** + **Resume**（复制恢复命令，按钮文字变"Copied"）+ 终端图标（在会话终端里打开）+ "Continue in"（换 Agent 继续）+ 删除。点击行展开详情与子代理树。
- **搜索**能搜会话内容（服务端读对话文件），输入法组合中不触发（`assets/plugins/sessions/client.js:42-46, 694-703@e66d872`）。
- **速度格**（`assets/plugins/sessions/client.js:1994-2120@e66d872`）：最近两分钟每秒 tok/s 的迷你曲线 + "42 tok/s"（进行中）或 "Avg 37 tok/s"；点开弹层给大数字、带间隙标注的曲线（"Tool 12s" = Agent 在两次请求间干活）、本轮均值 / 最快一秒 / 已输出 token。
- 大列表分页绘制（每文件夹 100 行，"Show N more"）（`assets/plugins/sessions/client.js:1233-1253@e66d872`）；首次建索引时有进度 hero（百分比、文件数、预计剩余、轮换小贴士，`assets/plugins/usage/page.js:4448-4595@e66d872`）。

### 2.6 Context 页（Usage › Context）与上下文卡

- **上下文卡**（`assets/context.js:425-964@e66d872`）：头部（标题 + "{tokens} / {window} · 62%" + 请求方 › 模型 + 状态胶囊 Live / Counted / Estimated）→ 两个大数字块（**Context used**：`128K / 200K tokens`、`62% full · ~18 requests of headroom`、健康词 Healthy / Filling up / Near full；**Prompt cache**：`88% hit` + 细条 + "176K cached · 24K new"）→ **可视化**（两种外观可切："Context map" 树图，每个条目一块，面积 = token 数，剩余空间是斜纹块；"Block grid" 20×20=400 格，每格 1/400 窗口，颜色 = 填充它的部分）→ 图例（System prompt / Tools / Skills / Memory / Files / Tool results / Conversation / Free space，悬停联动高亮）→ 内容列表（按部分分标签）→ 页脚（面包屑、N turns）+ **本会话的填充折线**（每个点是一个请求，可点）。
- 颜色按部分固定（`assets/context.css:3-19@e66d872`）；条目太小就并进"其余小块"以保证可点（`assets/context.js:316-393@e66d872`）；空格数与"余量"按历史每轮增量中位数估算（`assets/context.js:133-144@e66d872`）。
- **Context 标签页**（`assets/context.js:1013-1170@e66d872`）：每个 Agent 一张卡：评分环（Excellent / Good / Fair / Needs care）+ 四个子分条（Cache / Lean start / Pace / Reliability，各带满分标准说明）+ 标签芯片（Cache-friendly / Heavy start / MCP-heavy / Tool-heavy / Fills fast / Runs near full / Compacts / Flaky …每个标签 hover 一句解释）+ 各部分占比条 + 会话列表（每行一条上下文堆叠条）。
- **Tuned for you**（`assets/context.js:1170-1500@e66d872`）：用用户自己的历史回放不同设置（自动压缩阈值、缓存 TTL），给出"本可少花多少 token"的建议卡。

### 2.7 Quota（额度）表达与提醒

| 表达 | 位置 | 证据 |
| --- | --- | --- |
| meter：名称 + 百分比 + 4px 进度条 | 所有额度卡 | `assets/app.js:19012-19060@e66d872`、`assets/app.css:1229-1230@e66d872` |
| 重置行："Resets 14:26" / "resets in 2h" / "in 3d" | 卡片、账号行 hover、key 限额行 | `assets/app.js:15085-15102@e66d872` |
| **Pace**："Pace +12%"（比匀速多用 12%，超速时琥珀色） | 重置行 | `assets/app.js:14926-14953@e66d872` |
| 曲线：本周期剩余量 vs 匀速消耗对角线，阴影表示领先/落后；可切到过去周期 | 卡片内、托盘 sparkline | `assets/app.js:17214-17386, 17394-17412@e66d872` |
| 多账号合并条 + "N couldn't be read" | 卡片 "All N accounts"、组卡每个 provider 一行 | `docs/subsystems/gui-shell.md:414-454@e66d872`、`assets/plugins/routing/client.js:6938-6962@e66d872` |
| key 限额行："1.2M of 2M tokens · 800k left · resets 14:00 · 1 in flight" | Gateway keys 每行下方 | `assets/plugins/gateway/keys.js:1234-1258@e66d872` |
| 提醒：额度达到阈值 / 余额不足 / **重置前提醒**（周或月窗口用量不到 85% 即将重置，或重置卡将过期未用）/ 任务完成 / Bark 推送与每日汇总 | Settings › Allowances › Alerts | `assets/index.html:385-411@e66d872`、`assets/i18n.js:1569-1583@e66d872` |

### 2.8 Caller keys（Gateway keys，局域网共享）

- 入口在 Gateway 页（`assets/plugins/gateway/client.js:900-903@e66d872`）：标题行右侧 "Account groups" 与 "Add gateway key"，说明"每个客户端一个 key，用量分别统计"。
- **行**（`assets/plugins/gateway/keys.js:209-257@e66d872`）：启用勾 + 名称（点击改名）+ 掩码 + 复制按钮 + **一排设置药丸**（模型 / 账号 / 并发 "3 at once" / 档位 / 路由 / 脱敏 / MCP / 记忆 / 管理员 / 限额），未设置的药丸在悬停前隐藏；行下方是限额用量行。
- 点药丸就地展开编辑器，**保存前不发请求**（`assets/plugins/gateway/keys.js:1260+@e66d872`）；数字输入支持 "2M"、"100万"、"1亿" 等写法（`assets/plugins/gateway/keys.js:1162-1168@e66d872`）。
- 没开共享时，整块隐藏并留一句说明 key 的用途和何时出现（`assets/plugins/gateway/client.js:905@e66d872`）。

### 2.9 Settings

- 顶部：标签分段 + 右侧搜索框（`assets/index.html:180-190@e66d872`）。
- 行样式仿 macOS 系统设置：左边名称（必要时一行副说明），右边分段控件 / 下拉 / 开关；说明在名字已足够时不显示（`docs/subsystems/gui-shell.md:800-801@e66d872`，`docs/screenshots/font-settings.png`）。
- **说明按需显示**：解释性文字默认隐藏，指针悬停或键盘聚焦该行时以浮卡显示在行名下方，行不位移（`assets/settingshints.js:1-14@e66d872`）。
- **搜索**（`assets/finder.js:19-76@e66d872`、`assets/app.js:20198-20236, 23806-23816@e66d872`）：带**多语言概念词典**（输入"翻墙"→ proxy，"监控"→ observability，"费用"→ currency/cost…），容一个字母的拼写错误，中文长词按两字切分；不命中的行直接隐藏；其他页面上的匹配项列在后面（按页面名分组），点 Open 跳到该页并高亮那一行。⌘K 可从任何页面唤起。
- 隐私：脱敏总览、按类型 Mask / Warn / Block、试一试、共享开关、是否读取 Agent 登录信息（`assets/index.html:483-500@e66d872`）。
- 外观：跟随系统 / 浅色 / 深色；强调色 7 个预设；背景图；色盲模式；减少动效；文字大小；界面字体与代码字体（`assets/appearance.js:10@e66d872`、`assets/theme.js:1-13@e66d872`）。
- i18n：英文原文即 key，zh / zh-TW / ja / de 四种翻译，缺 key 回退英文（`assets/i18n.js:5, 5616, 11210, 16806, 22611@e66d872`）。
- 读取失败：整页说明并 "Try again"，自动重试（`assets/settingsunread.js:1-14@e66d872`）。

---

## 3. 可借鉴交互模式 Top 12

每条：是什么 → 证据 → 解决什么问题 → aigate 落点。

| # | 模式 | 证据 | 解决的用户问题 | aigate 落点 |
| --- | --- | --- | --- | --- |
| 1 | **路由决策叙事化**：先一句话结论，再按时间顺序逐句讲"请求了什么 → 为什么排第一 → 每次尝试结果 → 厂商原话" | `assets/plugins/routing/client.js:1019-1175, 3826-3926@e66d872`；`assets/plugins/routing/routing.css:255-267@e66d872`；`routing-zh-light.png`、`intent-trace-zh-light.png` | "为什么走了这个渠道/为什么失败"看不懂 | `web/src/components/RouteAttempts.vue`（见 4.1） |
| 2 | **模式 chip 即入口 + 一句话解释**：当前路由模式显示为可点 chip，菜单里每个选项带说明；选分段控件后下方一句话讲它做什么 | `assets/plugins/routing/client.js:6484-6503, 7676-7707@e66d872`；`intent-rules-zh-light.png` | 不知道"智能/按顺序/权重"到底什么意思，也不知道去哪改 | `RouteEditor.vue`、`PlatformView.vue` catalog 行 |
| 3 | **额度窗口三件套**：进度条 + 重置倒计时 + Pace（比匀速快/慢多少） | `assets/app.js:14926-14953, 15085-15102@e66d872`；`assets/plugins/gateway/keys.js:1234-1258@e66d872` | 只知道"花了多少"，不知道"会不会在重置前用完 / 会不会浪费" | `PeriodicBudgetPanel.vue`、`KeysView.vue`（见 4.2、4.5） |
| 4 | **卡片 ⇄ 表格双视图 + 已用/剩余切换**：少的时候看卡，多的时候看表；列按"窗口时长"而不是厂商叫法归并 | `assets/quotatable.js:30-36, 80-99, 101-274@e66d872`；`docs/subsystems/gui-shell.md:473-514@e66d872` | 十几个账号的卡片难扫描 | `PeriodicBudgetView` 的目标列表 |
| 5 | **上下文占用可视化**：已用/窗口大数字 + 健康词 + 余量估计 + 树图/格子图 + 缓存命中 | `assets/context.js:83, 133-144, 251-262, 425-964@e66d872` | 不知道长对话为什么贵、什么时候会触发压缩 | `RequestsView.vue` 请求明细（见 4.3） |
| 6 | **点击值即弹出可搜索 picker**：搜索框可直接输入任意 id；左侧分组导轨；每项带来源与实时徽标 | `assets/app.js:4870-4971, 5323-5355, 5483-5594@e66d872`；`picker-light.png` | 模型/渠道很多时找不到、分不清 | `PlaygroundView.vue`、`RouteEditor.vue` 的下拉（见 4.6） |
| 7 | **流式输出速度**：tok/s 迷你曲线 + 弹层（均值 / 最快一秒 / 已输出），算法对"被缓冲的突发"不报速度 | `assets/plugins/sessions/client.js:1994-2120@e66d872`；`docs/subsystems/gui-shell.md:115-188@e66d872` | 感觉慢但说不清是首 token 慢还是生成慢 | `PlaygroundView.vue`（见 4.9） |
| 8 | **诚实数字**：未知价格/读失败的数量单独标出，绝不并入 0；估算值带 ≈ 并解释口径 | `assets/plugins/usage/page.js:615-628, 976-1041@e66d872`；`docs/subsystems/gui-shell.md:436-438@e66d872` | 合计看起来精确其实漏算 | `OperatingReport.vue` 头部（aigate 文字已做到，可借其紧凑形式） |
| 9 | **全局设置搜索**：概念词典 + 容错 + 跨页命中 + 跳转高亮 + 返回 | `assets/finder.js:19-76@e66d872`；`assets/app.js:23757-23816@e66d872` | 页面多了以后找不到某个设置 | `App.vue` 顶栏加 ⌘K（见 4.8） |
| 10 | **解释按需显示**：规则一行常驻，长解释悬停才出现且不挤动布局 | `assets/settingshints.js:1-14@e66d872`；`docs/subsystems/gui-shell.md:800-801@e66d872` | 满屏警示框让人麻木，真正重要的一条被淹没 | 全站 `el-alert` / `section-note` 治理（见 4.4） |
| 11 | **空态画出形状 / 引导卡由真实状态打勾** | `assets/plugins/routing/client.js:6136-6170@e66d872`；`assets/app.js:1764-1930@e66d872` | 空页面不知道下一步做什么 | `OverviewView.vue` 的"运营准备清单"改为状态驱动（见 4.10） |
| 12 | **按数量自动折叠与渐进披露**：≥3 折一行、先画 8 行、次要动作进菜单 | `assets/plugins/routing/client.js:1954-1957, 5236+@e66d872`；`assets/plugins/gateway/keys.js:209-257@e66d872` | 行太长、表太宽，手机上溢出 | `KeysView.vue`、`PlatformView.vue` 操作列（见 4.5、6.3） |

---

## 4. aigate 控制台改进建议（按优先级）

aigate 现状基线（供对照）：Vue 3 + Element Plus，**32 个控制台路由**（工作空间 10 + 账号安全 1 + 平台 21，`web/src/router.ts:17-38`、`web/src/App.vue:16-50`），管理页基本是 PageHeader + metric 卡 + `el-table` + 对话框；`web/src` 内**没有任何图表代码**（无 svg / canvas / echarts），没有暗色主题，没有设置搜索。页面把大量合规说明常驻为 `el-alert` 与 `section-note`。

"后端数据"一栏依据前端类型定义判断（`web/src/request-wire.ts`、`periodic-budget.ts`、`procurement.ts`、`alerts.ts`、`key-access-policy.ts`），未核对 Go 侧实现。

### P1：数据已有，纯前端可做

**4.1 RouteAttempts：从"表格堆叠"改成"结论 + 有序候选 + 逐次尝试"**（后端：已有）
- 现状：每次尝试一张事实 `dl` + 一张候选表，原因全部是同权重的 tag（`web/src/components/RouteAttempts.vue:44-57`）。
- 改法：
  1. 顶部加一句话结论，由已有字段拼出：`请求 → 尝试 N 次 → 命中 {渠道·上游模型}`；未准入时直接写"没有任何候选通过筛选：{原因计数前 3}"。数据在 `route-decisions[].candidates[].reason`（`RouteAttempts.vue:9-10`）。
  2. 候选区按"选中 → 粘性覆盖 → 被排除（按原因分组，可折叠）"排序；被排除的用一行芯片"capacity_full ×2 · circuit_open ×1"，点开才展开明细。
  3. 每次尝试给一行"发生了什么"：`sequence + created_at + delivery_state + degradation`，用时间线（Element Plus `el-timeline`）代替卡片堆叠。
- 对应 magpie：1、叙事化（`assets/plugins/routing/client.js:3826-3926@e66d872`）。

**4.2 周期预算：加进度条、重置倒计时、Pace**（后端：已有）
- 现状：四个数字（限额 / 已计入 / 占用 / 跨周期占用）+ 窗口起止文本（`web/src/components/PeriodicBudgetPanel.vue:149-150`）。
- 改法：把"当前周期已计入 + 占用"合成一根进度条（已结算深色、占用浅色两段），条右写"距重置 6h 12m"（`current_window.end_at` 已有），下方一行"Pace −8%"= 已用比例 − 已过时间比例，超过匀速用 warning 色；"周期窗口"表加一列迷你条。对"首次跟踪的不完整周期"不给 Pace。
- 对应 magpie：3。

**4.3 请求明细：加上下文占用与健康度**（后端：占用比已有；分项拆解没有）
- 现状：`dl` 列了 20 个字段（`web/src/views/RequestsView.vue:60`），看不出"这次请求在窗口里占多大"。
- 改法：在"完整用量"旁加一行堆叠条：输入 token / 模型 `context_limit`，标 `62% · 128K / 200K`，右端给健康词（<75% 正常 / <90% 偏满 / ≥90% 接近上限）。数据：`retail_quantities.text_input_token` + 模型 `context_limit`（`web/src/views/ModelsView.vue:65`）。
- 不建议照搬的部分：magpie 把 system / tools / files 等逐项拆开并缓存请求正文供查看（`docs/subsystems/gui-shell.md:21@e66d872`），这与 aigate"审计不展示原始模型正文"的原则冲突（`web/src/views/PlatformView.vue:57`）；若要做分项，应只让网关汇总各类 token 数、不落正文。
- 对应 magpie：5。

**4.4 说明文字按需显示**（后端：无）
- 现状举例：`OperatingReport.vue:71-75` 四段常驻说明 + 三类 `el-alert`；`ReportsView.vue:105` 长注；`RouteEditor.vue:85-89` 五条常驻/条件 alert；`PlatformView.vue:329` 每页一条 `section-note`。
- 改法：每页保留**一条**最关键的规则（例如"未知成本不按零计入"），其余改成标题旁的"说明"图标（`el-tooltip` / `el-popover`），或折叠进"口径与限制"。阈值类数字（31 天 / 5,000 请求）放到字段 hint。
- 对应 magpie：10。

**4.5 密钥列表：行内药丸 + 行下用量行，次要动作收进"更多"**（后端：已有）
- 现状：9 列 + 330px 固定操作列（`web/src/views/KeysView.vue:326-338`）。
- 改法：来源地址 / 模型范围 / 地域 三个 popover 改为行内药丸（"3 个来源""全部模型""1 个地域"），未设置的灰显；"累计消费 / 上限"改成行下一行"`已花 / 上限 · 冻结 X · 距到期 N 天`"并配细条；操作只留"配置"与"更多"（来源策略 / 运行限额 / 撤销）。
- 对应 magpie：3、12（`assets/plugins/gateway/keys.js:209-257, 1234-1258@e66d872`）。

### P2：需要少量后端或纯结构调整

**4.6 下拉选择器附注**（后端：注解数据已有；速度没有）
- `PlaygroundView.vue:116` 的模型下拉每项只显示"名称 · 协议"；可在选项里加第二行（上下文上限、是否仅输入计费、本租户输入/输出价）。`RouteEditor.vue:97` 的渠道下拉可标注地域与启用状态。
- magpie 的 `tok/s` 徽标依赖按模型统计的 TTFT/速度接口（`assets/app.js:5483-5492@e66d872`）；aigate 请求行没有首 token 时间（见 4.9），暂不做。

**4.7 经营报表：紧凑 KPI 条 + 份额条**（后端：已有）
- 现状：11 张 metric 卡 + 4 张宽表（`web/src/components/OperatingReport.vue:85-155`）。
- 改法：11 张卡压成一条 KPI 带（大数字 + 一行小字），未结算/未知成本两项用 warning 色并与下方 alert 合并为一句；"按模型汇总""按渠道汇总"两表各加一列份额条（请求占比），一眼看出主力和长尾。
- 对应 magpie：Overview 排行行的"份额条"（`assets/plugins/usage/page.js:1196-1200@e66d872`）。

**4.8 ⌘K 导航与设置搜索**（后端：无）
- aigate 平台侧 21 个页面分 4 组（`web/src/navigation.ts:2-7`），没有搜索。可在顶栏加一个命令面板：输入即过滤页面名与别名（例如"核款"→ 人工核款，"成本"→ 渠道成本版本 / 运营与成本报表），回车跳转。不需要 magpie 那样的概念词典，先做页面名 + 每页两三个别名即可。

**4.9 Playground：实时 tok/s**（后端：联调页可纯前端估算；调用记录需新增采集）
- 现状只有首字节 / 首有效内容 / 总耗时（`web/src/views/PlaygroundView.vue:144`）。
- 改法：流式过程中按已到达的增量估算 tok/s（粗略按字符或等 usage 到达后校准），结束后显示"首内容 X ms · 平均 Y tok/s"；并提示"含网络与网关等待"。
- 若要在调用记录里展示 TTFT / 速度，需要网关在尝试级记录首 token 与首内容时间，并进入请求行——**后端目前没有**。

**4.10 引导清单状态驱动**（后端：已有）
- `OverviewView.vue:40` 的"运营准备清单"是静态三步。可改成读取 `/platform/channels`、`/platform/models`、`/platform/tenants` 的数量自动打勾，并显示"还差什么"。
- 对应 magpie：11。

**4.11 平台导航减负：把同属"资金单据"的页面并成一页多标签**（后端：无）
- 现状：人工核款、消费退款、客户钱包申请、收费核对、现金退款、资金来源与核对 六个页面结构相同（表格 + 明细 + 审批）。
- 改法：保留路由不变，但在侧栏只留一个"资金单据"入口，页内用标签切换；magpie 的 Usage 页正是这种"一个导航入口 + 四个标签"的组织（`assets/plugins/usage/client.js:19-31@e66d872`）。

### P3：需要新后端能力

**4.12 调用记录图表与缓存命中率**（后端：缺聚合接口与缓存维度）
- magpie 的按小时/天叠柱、53 周热力图依赖服务端聚合（`assets/plugins/usage/page.js:1126-1149, 3118-3186@e66d872`）。aigate 的计量维度只有 `text_input_token` / `text_output_token`（`web/src/metering-wire.ts:8-25`），没有供应商侧缓存读写维度，`cache_hit` 仅表示"同租户精确缓存"。要做缓存命中率需先在计量合同里增加维度，属于规格/设计变更。

**4.13 预算类提醒**（后端：缺告警种类与外发通道）
- magpie 有"额度达阈值""余额不足""重置前提醒"（`assets/index.html:385-411@e66d872`）。aigate 现有种类为渠道隔离、采购预算不足、待核对积压等（`web/src/alerts.ts:127`），且明确"无外发通知"（`web/src/views/AlertsView.vue:47`）。可考虑新增"客户预算周期即将耗尽""预算周期将重置且用量偏低"，但外发通道是独立的产品决定。

**4.14 路由"按请求回放"**（后端：缺尝试级时间戳精度）
- magpie 的舞台动画依赖 trace 里每次尝试的起止时间。aigate 的 attempts 有 `created_at` 与 `sequence`，可做静态版"请求 → 候选 → 尝试 1/2/3"的步骤条；带耗时的回放需要更细的时间字段。

---

## 5. 视觉风格结论与"不搬"清单

### 5.1 magpie 的视觉语言

| 维度 | 观察 | 证据 |
| --- | --- | --- |
| 配色 | 浅色：底 `#f4f4f6`、卡 `#fff`、字 `#1c1c21`、线 `#e3e3e8`；强调靛蓝 `#4f46e5`；深色：底 `#1a1a1e`、卡 `#232327`、强调 `#a5b4fc`。红绿琥珀只用于状态，红色偏柔和（`#d64545`） | `assets/app.css:1-99@e66d872` |
| 排版 | 系统字体栈（SF Pro / Segoe UI），**基础 13px**，行高 1.4；副行 11.5px；小字 10.5–12px；KPI 大数字 20px、Routing 统计数字 19px；等宽只用于 id / 模型名 / 金额上下文 | `assets/app.css:36-38, 153-165, 786-788@e66d872`；`assets/plugins/routing/routing.css:229@e66d872` |
| 密度 | 行高 40px（窗口 48px）；卡片内边距 10–14px；大量信息靠"副行 + 悬停提示"承载，表面很空 | `assets/app.css:776-777, 1137@e66d872` |
| 圆角与边框 | 1px 发丝线，几乎无阴影；卡片 10–14px，输入框 7px，分段控件 7px，弹出层 11px + 大投影 + 背景模糊 | `assets/app.css:770, 1137, 1460, 3956, 2588@e66d872` |
| 控件 | 自绘分段控件（滑块 thumb）、"弹出按钮"式字段（药丸 + 下箭头）、自绘开关；**没有原生 select**；状态用点不用彩色左边条 | `docs/subsystems/gui-shell.md:817@e66d872` |
| 图标 | 大量使用**厂商真实彩色 logo**（Agent、供应商、模型），深色下转单色；自绘 16px 线性图标 | `assets/app.css:203-213@e66d872`；`providers-dark.png`、`picker-dark.png` |
| 动效 | iOS 式弹出（从锚点放大）、数字滚动、列表展开弹簧、侧栏选中滑块；全程尊重"减少动效" | `assets/app.css` 中 37 处 `prefers-reduced-motion` |
| 主题 | 浅/深/跟随系统，强调色可换并给底色染 5–8% 色相，色盲模式切换图表序列 | `assets/appearance.js:10@e66d872`、`docs/subsystems/gui-shell.md:34@e66d872` |

### 5.2 不应搬到 aigate 的部分

aigate 必须保持自己的 Element Plus 视觉体系：青绿主色 `#087e83`、深色侧栏 `#102236`、DM Sans / Noto Sans SC、`--el-border-radius-base: 7px`（`web/src/style.css:1, 3`）。以下属于 magpie 的品牌与外观，不搬：

1. 喜鹊 logo、启动时翅膀/尾巴动画（`assets/index.html:66@e66d872`、`assets/app.css:275-300@e66d872`）。
2. 厂商真实彩色 logo 墙与"logo 即拖拽把手"的行样式。
3. 磨砂托盘面板、macOS 红绿灯内嵌的标题栏、Windows 自绘标题按钮。
4. 自绘分段控件 / 自绘开关 / 自绘 popover（替代 `el-select`、`el-switch`）。
5. 靛蓝强调色、SF Pro 字体栈、13px 基础字号、无阴影发丝线卡片风。
6. "路由舞台"的喜鹊飞行动画与 SVG 流动连线（aigate 是多租户平台，全局实时流不合适；见 4.14）。
7. 可换强调色与背景图。
8. 底部 Tab 栏的整体外观（可借鉴其信息设计，不必照搬样式，见第 6 节）。
9. Omarchy 主题跟随（`site/public/img/omarchy/` 下 20 余套配色）。

### 5.3 中性的信息设计原则（可借鉴）

- **数字优先**：大数字 + 小标签 + 一行副注，解释放进悬停。
- **未知 ≠ 0**，估算带 ≈ 并说明口径。
- **状态不只靠颜色**：点 + 形状（失败画 ×）+ 文字。
- **每行一个主操作**，其余进菜单；未设置的属性不占位。
- **渐进披露**：先 8 行、≥3 折一行、长解释按需出现。
- **空态给下一步**，引导卡由真实状态驱动。
- **全站同一个概念同一种画法**（额度窗口四处同形）。
- **尊重减少动效、色盲序列、4.5:1 对比度**（`assets/app.css:76-80@e66d872` 注释明确要求）。

---

## 6. 移动端结论

### 6.1 magpie 有没有手机布局

有，但只对 **`magpie web`（浏览器访问）** 生效；桌面 App 的窗口只是窄窗响应式（例如 600px 以下 Agent 行折行，`assets/app.css:5619-5640@e66d872`）。

| 设计 | 证据 |
| --- | --- |
| 断点：`(max-width: 760px), (pointer: coarse) and (max-width: 1024px)`；基础字号提到 14px | `assets/app.js:23948@e66d872`、`assets/app.css:4909-4910@e66d872` |
| **底部 Tab 栏**：图标在上、文字在下，最多 5 个，其余进 "More" 菜单（带图标与当前标记）；顶栏只留 logo 与动作 | `assets/app.css:393-429@e66d872`、`assets/phonetabs.js:18-52@e66d872` |
| **44px 触控目标**，但视觉尺寸更小：图标按钮 44×44；分段控件画 28px 高、用 `::after` 把点击区扩到上下各 8px；卡片 ⋯ 画 22px、点区 44px | `assets/app.css:4911-4925@e66d872` |
| 悬停才出现的控件在触屏**常显**；卡片 ⋯ / 抓手 / 未设置药丸全部可见 | `assets/app.css:1403, 1514, 5043-5049@e66d872` |
| KPI 四格 → 两列；排行行重排成三行网格（名称+花费 / 数字 / 份额条） | `assets/app.css:4991-4998@e66d872` |
| 行尾药丸换到第二行，不挤压名称与开关 | `assets/app.css:5020-5025@e66d872` |
| 输入框 16px 字号（避免 iOS 自动缩放）、最小高 44px；弹窗内表单单列 | `assets/app.css:5006-5009@e66d872` |
| Usage 页头在手机上重排：标签占满一行，周期改为菜单，刷新变成"圆点 + 间隔" | `assets/app.css:4957-4972@e66d872` |
| 安全区：`env(safe-area-inset-*)`；添加到主屏幕以独立应用打开（manifest + iOS meta） | `assets/index.html:12-25@e66d872`、`assets/app.css:4913@e66d872` |
| 触屏打开 picker 不自动弹键盘 | `assets/app.js:4969-4971@e66d872` |
| 不用容器查询，改用按宽度加类（`max560/max520/max420`），因为 WebKit 下容器查询会把滚动的视图拉回 | `assets/plugins/routing/client.js:338-345@e66d872`、`assets/context.js:198-209@e66d872` |

### 6.2 aigate 现状

- ≤800px：左侧栏变抽屉，带焦点圈定、Escape、背景锁、`inert`、返回焦点；触控行约 44px（`web/src/style.css:6`、`specs/038-new-api-capability-migration/navigation-refinement.md:30-46`）。这套可访问性处理比 magpie 完整，应保留。
- ≤520px：metric 单列、页头纵排（`web/src/style.css:7`）。
- 没有底部 Tab 栏；表格页在 390px 下依赖 Element Plus 表格的横向滚动（未实际渲染验证）。

### 6.3 给 390px 验收的可借鉴点

1. **工作空间侧加底部 Tab 栏**（工作台 / 模型 / 密钥 / 联调 / 记录，其余进"更多"），平台侧保留抽屉。工作空间只有 10 个页面且高频入口固定，正适合 magpie 的"5 个 + More"模型。
2. **"画得小、点得大"**：分段控件、芯片、卡片 ⋯ 保持现在的视觉尺寸，用伪元素扩大点击区到 44px，可同时满足"不显笨重"与触控验收。
3. **触屏常显悬停控件**：aigate 现有页面的操作按钮都是常显的（检索 `web/src` 未发现悬停才出现的控件），这一点已满足；它应作为新增组件（行内药丸、⋯ 菜单、份额条）的约束写进验收，避免照搬桌面端的悬停习惯。
4. **行尾换行规则**：`KeysView.vue` 的 330px 固定操作列是 390px 下最主要的横向溢出源，按 4.5 收进"更多"后表格可回到两三列。
5. **输入框 16px** 防 iOS 聚焦缩放，配合现有的 `el-input` 全局字号（当前 `--el-font-size-base: 13px`）需要在窄屏下单独覆盖。
6. **打开选择器不自动弹键盘**：aigate 现有筛选输入没有自动聚焦（仅登录页 `autofocus`），无需改动；若后续新增 ⌘K 面板或带搜索的 picker，触屏上应等用户点搜索框再弹键盘。
7. **safe-area**：若后续做 PWA，再补 `viewport-fit=cover` 与 `env()`；当前可不做。

---

## 7. 不确定点

1. **截图与当前代码不一致**：仓库截图（`site/public/img/*.png`）是营销截图，显示的是**顶部标签**布局；当前代码在 ≥1024px 默认是**左侧栏**（`assets/app.js:24010-24187@e66d872`）。侧栏的实际外观只有代码与文档依据，没有看到渲染图。
2. **未实际渲染**：上下文树图 / 格子图、舞台飞线、回放条、磨砂弹出层、托盘菜单栏单元格（macOS 原生绘制）的效果均由代码与少量截图推断。
3. **阅读覆盖**：`app.js` 只读了 Agents、Providers、额度、设置相关函数；`plugins/routing/client.js` 读了页面骨架、模式文案、故事、组卡、组编辑器的一部分，未读 `render()`（3250 行起）、`renderActs` 主体、回放与面板实现；`plugins/library/client.js`（589 KB）只读了入口与标签定义；`plugins.js`（Plugins 页）、`plugins/redact/*`（脱敏页）只读了头部注释与截图。
4. **Go 侧**只看了函数签名与少量头部注释（`internal/gui/trace.go`、`context.go`、`usage.go`、`caller_keys.go`、`modelprice.go`、`groups.go`、`quota_alert.go`），没有核对接口返回的完整字段。
5. **`docs/reference.md`**（4,808 行）只读了"Routing groups"一节与标题目录；`docs/subsystems/gui-shell.md` 精读至第 1240 行，其后为更新与交接逻辑，与界面无关，未读。
6. **i18n**：`assets/i18n.js` 只抽样了 zh 的提醒段落与结构；未核对四种语言覆盖率。
7. **aigate 侧**：只读了前端；"后端数据是否已存在"依据 TypeScript 类型定义推断，未核对 Go 后端是否已采集但未暴露（尤其是 TTFT、供应商缓存 token）。`web/src` 中"无图表代码"的结论来自文件内容检索。
8. **aigate 390px 的实际渲染**未验证（未启动应用），6.3 的第 4 条是依据列宽定义的推断。
9. **magpie 页面数量**的口径：一级页面 11 个是确定的；"约 32 个标签级视图"含 Library 9 个标签、Settings 9 个标签等，属于按标签计数，不代表同等复杂度。

---

### 附：一句话结论

magpie 最值得学的不是它的外观，而是三件事：**每个决策都配一句人话**、**每个额度窗口都有进度 + 重置 + 节奏**、**解释默认藏起来、需要时才出现**。这三件事在 aigate 现有的 Element Plus 体系内都能用已有的后端数据做到。
