# 020 API与界面合同

2026-11-01，**已实现 / 限定本地验收通过**。实现与实际验收边界见[验证记录](../../docs/verification/public-catalog.md)；统一架构和最终集成检查由集成人完成。所有新接口仅允许已列HTTP方法。错误统一 `{error:{code,message},request_id}`，不得反射查询值、profile或数据库错误。

## 匿名目录

`GET /api/public/models`（可选`q`、`protocol`、`cursor`）

- 不经过session/MFA/API Key中间件。原始query最多4096字节；用完整ParseQuery错误检查，拒畸形百分号编码，不接受URL.Query静默丢弃坏参数。query仅允许q、protocol、cursor，每项至多一次；空q/protocol等同不筛选，显式空cursor非法。拒未知/重复参数、非法UTF-8或控制字符。
- q为去除首尾空白后的模型名**前缀**，最多100 UTF-8字节；SQL转义`%`、`_`、反斜杠为字面量，不接受正则表达式。大小写敏感，与逻辑模型名实际匹配一致。
- protocol为现有六协议之一：`openai_chat|openai_responses|anthropic_messages|openai_embeddings|jina_rerank|gemini_generate_content`。
- 固定每页50，额外读取第51项决定next_cursor；不提供客户端任意limit或offset。非空cursor最多1024字节，必须满足无padding的base64url解码后重编码相同；解码后≤768字节、合法UTF-8，内部精确对象`{v:1,name,id,filter}`，拒未知/重复/尾随/非规范类型。name为1..100字节合法逻辑名、id小写非nil UUID；filter为固定顺序结构`{q,protocol}`在q去首尾空白、空protocol归一后规范JSON的SHA-256小写hex，必须匹配本次筛选。摘要用于绑定筛选，不是授权签名；任何游标都重新执行公开/启用谓词。
- 成功：`{data:PublicModel[],request_id,next_cursor:string,as_of:string}`。as_of为本页SQL唯一数据库UTC价格截止时刻，空列表也返回该DB时刻；next_cursor为空表示本页已到末尾。as_of不证明响应发出时的最新配置或跨页MVCC快照。

`GET /api/public/models/:id`

- :id为小写非nil UUID；无查询参数。
- 成功 `{data:PublicModel,request_id,as_of}`。模型不存在、私有或已停用均404 `not_found`，不区分原因；格式错误400 `invalid_public_catalog_request`。

`PublicModel`精确白名单：

```json
{
  "id": "11111111-1111-4111-8111-111111111111",
  "public_name": "example-chat",
  "protocol": "openai_chat",
  "capabilities": {
    "streaming": true,
    "tools": true,
    "input_only": false,
    "text_only": true
  },
  "limits": {
    "context_tokens": 128000,
    "input_token_upper_bound": 120000,
    "max_output_tokens": 8000,
    "default_output_tokens": 2000
  },
  "pricing": {
    "status": "available",
    "currency": "CNY",
    "unit": "micro_cny_per_million_tokens",
    "price_version_id": "22222222-2222-4222-8222-222222222222",
    "input_rate": "1000000",
    "output_rate": "2000000",
    "effective_at": "2026-11-01T00:00:00Z"
  }
}
```

limits均来自当前有效profile，是0..10,000,000的JSON整数，前端要求安全整数。`context_tokens/input_token_upper_bound`须正且输入界≤context；生成模型满足`0<default_output_tokens<=max_output_tokens<=context_tokens`，输入计费协议两输出字段为0。`capabilities.text_only`指本版仅接受文本输入，不表示Embeddings/Rerank返回文本生成结果。空protocol按既有Profile.ProtocolName规范为openai_chat。tools仅在协议合同支持且profile允许时为true；streaming只对四种生成协议为true。公开profile额外限定序列化文本≤16KiB，以约束匿名读取/解析内存，不改变私有模型的既有存储合同。

所有价格即使为0仍是规范字符串。unavailable仅表示不存在当前有效价：保留currency/unit，`price_version_id,input_rate,output_rate,effective_at`全部null，不混合半份价格。存在价格但类型/币种/非负范围损坏，或输入计费协议输出价非0时不能伪装unavailable，应503。错误/不受支持profile不降级为编造能力：公开配置拒绝发布；异常公开行导致本次503并仅记录安全request_id诊断。

所有匿名响应带 `Cache-Control:no-store`；不写Set-Cookie、不返回CSRF/session字段。非法列表query/cursor为400 `invalid_public_catalog_request`，限流/在途许可满为429 `public_catalog_rate_limited` 且带 `Retry-After:1`；共享metadata的ready/Guard拒绝保留现有503 `instance_unavailable`，DB/投影无效为503 `public_catalog_unavailable`。不得为目录GET调用任何Provider方法。未注册方法保持原API拒绝行为，不回落为成功SPA或执行写入。

## 管理公开配置

| 接口 | 输入/响应 |
|---|---|
| GET `/api/platform/models/:id/publication` | admin；`{data:{id,public_name,enabled,public_visible,public_revision},request_id}`；可读停用模型，无需近期再认证 |
| PUT `/api/platform/models/:id/publication` | admin；严格JSON `{expected_revision:"1",public_visible:true}`；成功返回同一安全View |

PUT必须显式boolean，null/遗漏/字符串非法；revision是规范正int64字符串，拒JSON number、符号、前导0、溢出。≤8KiB，拒未知/重复/大小写别名/尾随JSON；路径规范UUID。沿CSRF/近期再认证/已启用MFA/ready/Guard/恢复门禁，事务内再次确认actor仍为active admin。缺失404，旧CAS409 `public_catalog_revision_conflict`，非法请求/profile为400 `invalid_public_catalog_configuration`。

同标记且当前revision为no-op：不增revision、不写变更审计。旧revision即使请求值等于当前仍冲突。变更与一次`model.publication.update`安全审计同事务。PUT不新增持久命令ID或资金回执；网络不明只GET，不能自动PUT。

public_revision只保护公开标记，不保护enabled、profile或价格。并发停用与公开设置按model行锁串行，两者各自只更新自己的字段；最终`public_visible=true,enabled=false`是合法隐藏状态。后续重新启用会恢复公开，管理页必须明确显示该影响，不能把PUT成功一概写成“目录现已可见”。标记公开前的验证用锁后新语句读取的当前profile。

## 页面与未知结果

- `/pricing` 为独立公开路由；App公开布局仅含产品标识、价格目录与登录入口。登录页可直接打开价格目录；已有会话、MFA待验证或待绑定状态均不阻断匿名资料访问，进入私有路由仍走原身份门禁。
- 目录使用匿名专用fetch（credentials omit），不复用会触发`moni:unauthenticated/reauth`的私有请求行为。刷新或筛选变化增加epoch、清旧cursor；旧筛选/卸载/晚回不可追加新页。加载更多串行，按id去重；不拼接旧筛选或跨页价格总数。各页可能有不同as_of，前端为每批行保留其响应时刻并以行提示/页面分组表达，不能用最新一页as_of冒称全部已加载价格同时更新。
- 大金额沿字符串/BigInt CNY换算。unavailable显示“暂无有效售价”；input_only输出显示“固定为0，仅输入计费”；展示每页观察时刻与“调用时由服务端冻结价格”。不展示不存在的供应商认证或在线SLA。
- 管理编辑固定model ID/revision/身份epoch。409重读再编辑；结果不明禁用原编辑并保留核查提示，显式GET后区分当前状态与原请求归因，用户重新开始编辑才可提交新CAS。不得自动重试PUT，包括再认证完成之后。
