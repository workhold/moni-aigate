# 011：渠道模型发现与差异预览

2026-10-31，沿[能力路线图](../../docs/design/new-api-roadmap.md)NA-06/A3继续。状态：限定范围本地验收通过，见[验收清单](acceptance.md)与[验证记录](../../docs/verification/model-discovery.md)。先交付可独立验收的模型发现，再继续凭据池/批量运营；002/008性能专项后移。

## 用户出口

平台管理员在渠道页选择目录格式，读取当前渠道的供应商模型目录，查看未绑定模型、已绑定模型、目录缺失绑定以及历史发现结果。每次发现关联不可变渠道/凭据版本和开始时的已发布绑定快照，不调用生成、不动客户资金。目录是接入参考，不能证明具体模型的协议、额度、计量硬界或收费就绪。

## 固定合同

1. `POST /api/platform/channels/:id/discover-models`，body仅`format`（openai/anthropic/gemini）与必填`expected_version`。沿原session、CSRF、可选MFA、管理员、近期再认证、实例Guard与恢复门禁。旧版本、停用/隔离渠道或撤销凭据在网络前拒绝。每渠道每分钟最多一次，和平台探测共享4个运行许可、占全局100预算中的一个；许可覆盖全部分页与持久收尾。
2. 供应商只接收GET，固定使用配置base URL（含/v1或/v1beta）下的`/models`。OpenAI Bearer；Anthropic x-api-key+anthropic-version；Gemini x-goog-api-key。复用既有SSRF/DNS固定地址、出口CIDR、TLS、端口、禁环境代理与禁重定向，既有Endpoint不接受任意query。分页query仅由适配器按同origin固定端点构造，不跟随供应商提供的URL。
3. 整个读取≤10s且尊重更短客户deadline；每页解压后≤1MiB、全部≤4MiB、≤10页、原始条目合计≤1000。JSON必须UTF8且无重复键，响应Content-Type为application/json（可带参数）。失败不返回部分成功目录，不重试，不把目录错误用于推理健康、凭据撤销或绑定隔离。
4. OpenAI读取data/id的列表，识别has_more/hasMore及next_page/next/nextPageToken/next_page_token/next_cursor/nextCursor，显式未结束则拒绝，不猜测该厂商分页协议；Anthropic读取data/id、必需has_more，true时使用last_id构造after_id/limit=100；Gemini读取models/name，以安全名规范化models/前缀，supportedGenerationMethods存在时只保留generateContent，nextPageToken仅构造pageToken/pageSize=100。游标≤512字节、UTF8、无控制字符且不能重复；页数/条目上限前未结束则失败。这里的完整性限定于三种已支持schema及其分页字段，其他厂商分页需独立适配。目录空结果合法，不将其自动解释为应删除全部路由。
5. 仅保留排序去重的安全模型ID，不保存或返回原始响应、描述、任意URL或供应商错误。一般ID≤200 ASCII、首alnum、后续alnum/`.`/`_`/`-`/`/`/`:`，拒`..`、`//`、URL形状、空白/控制/`%`/`?`/`#`；Gemini继续使用010安全单段规则。模型ID属于不可信供应商数据，只作为文本显示与人工配置参考。
6. 开始事务依次锁渠道SHARE、当前凭据SHARE并重新验证版本/状态，持久化attempt、≤1000条已发布绑定快照和同事务审计；HTTP期间无数据库事务。发起前再次Guard及版本/状态检查。开始后正常轮转允许该次使用已固定版本，结果标旧；紧急撤销不能撤回已经发送的GET。
7. 收尾在独立≤5s清理上下文中追加唯一result+审计，保存completed/failed、受控error_code、页数与净化模型ID。attempt/result均不可变；失败结果不带部分目录。若持久化结果失败，返回discovery_pending并附discovery_id，不假称完成。进程中断或收尾缺失时不自动重发，历史在30s后把无result的记录显示为interrupted；这是读取时的派生状态。
8. `GET /api/platform/channels/:id/model-discoveries`分页返回元数据；`GET .../:discoveryID`返回详情。仅admin可读，channel与discovery必须匹配。详情`unbound`是目录中未在开始快照出现的ID；`present`/`missing`是开始快照中目录返回/未返回的绑定，可包含同上游模型的多个公开模型。missing仅表示该格式/凭据这次未列出，不等于模型不能调用。失败/运行中不生成差异。
9. stale动态判断当前渠道版本/可用性、原凭据撤销、已发布绑定快照变化；不能将旧结果显示为当前配置。绑定快照含公开模型ID/名称、enabled、revision、上游名、channel_version_id，并包含已停用公开模型的当前revision，防止将已配置但暂停模型误当未绑定；整个快照≤256KiB。stale不回写历史，预览无一键同步、创建、删除、授权、定价或发布。正式发布仍使用原完整绑定+expected_revision校验。
10. 控制台可选择三格式、读取/搜索/复制模型名、查看历史与差异及stale。渠道切换/关闭后旧异步结果不能覆盖新状态；不保存供应商秘密、浏览器无直连上游、不自动轮询或重试。失败通过discovery_id查历史。新增目录不推断protocol/profile或售卖价格。

## 数据、实现与验收

新增007迁移：channel_model_discoveries（开始事实）与channel_model_discovery_results（唯一终态）；数组与序列化字节均受约束、外键关联现有版本/凭据/操作者、不可变触发器及应用角色只读/追加权限。新表不进入客户财务或projection权限。既有迁移不改写。

Provider真实HTTP验证三格式/分页/认证/完整性/安全/取消与资源上限；真实PG+HTTP验证版本拒绝、快照/历史/动态stale、结果与审计原子性、不变历史、权限/CSRF/再认证/恢复、失败/中断、许可清理以及钱包/账本/路由/价格/授权/健康不变。前端契约与strict build、全Go race/vet/嵌入build和预览迁移/角色就绪分别记录。不调用付费模型、执行真实性能压测或修改评审账号/MFA。

本切片不含自动同步/后台定时、自动建模/发布、供应商余额查询、多凭据轮询、全供应商认证或New API数据迁移。后续凭据池须独立规定不可变池版本、调度/撤销/容量与attempt追溯，不将已有单凭据渠道伪称账号池。
