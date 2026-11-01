# 017 Key 基本配置编辑

2026-10-31，限定范围本地验收通过；[证据](../../docs/verification/key-configuration.md)。承接NA-07与015/016，独立实现名称、模型范围、地域、累计额度和有效期编辑。周期预算、分组商品、Key换发/恢复撤销、平台/租户动态限额不在本轮。

## 持久配置和API

1. 012追加迁移为api_keys增加configuration_revision BIGINT正值default1；触发器INSERT强制1，仅ROW(name,scopes,allowed_regions,lifetime_limit,expires_at)实际变化递增，直接指定版本无效。来源和运行限额及消费spent/held、撤销均与该版本互不干扰。不对历史五字段增加会阻断财务更新的新CHECK，不自动清洗历史数据。
2. 创建和列表补allowed_regions及字符串configuration_revision；其他创建合同保持，原名称/金额/有效期输入错误映射400而非内部503。创建不增加额外凭据，不重置秘密。GET/PUT /api/keys/:id/configuration。GET仅{id,name,scopes,allowed_regions,lifetime_limit,expires_at,configuration_revision,spent,held}九字段；金额为非负int64十进制字符串或null（limit），expiry为RFC3339或显式null。spent/held是只读观察，不是配置版本的一部分。
3. PUT完整必填{expected_revision:string,name:string,scopes:string[],allowed_regions:string[],lifetime_limit:string|null,expires_at:string|null}。缺少任意字段拒绝；null仅允许两个可空字段。创建与配置PUT上限64KiB，足够容纳1000个模型UUID；严格JSON、重复/未知/大小写别名/尾JSON拒绝，规范UUID/正int64版本。限额0表示零预算，null表示无累计上限；expiry null明确移除期限，不把空字符串当null。
4. 未修改字段按数据库原值保留，即使历史名称包含控制字符、scope为空/旧格式、地域为空或任意旧标签，也可原样读取及与其他字段一起提交，不因新规则阻断原财务/配置写。只有与锁后原值不同的字段才规范化和校验：名称trim后1..100 UTF-8字节且无控制字符；scopes为单独["*"]或1..1000个规范UUID，去重排序；地域1..32项、每项trim后1..80字节且无控制字符，去重排序，支持单独["*"]。global是精确标签，不表示全部地域。历史数组未改时保留顺序和内容，不能在打开编辑器时自动清洗。
5. 模型/地域是限制，不是授权。Key始终取租户、所属成员和自身授权交集；指定未授权UUID或地域不赋予权限，未来外层授权变化才可能生效。UI保留目录未列出的旧scope，不能静默丢弃。该接口不改变Key owner、租户、digest/prefix、来源或运行限额。
6. expiry使用四位年份、大写T/Z、双位时分秒及最多9位小数的RFC3339格式，先严格校验语法、日期与时区（拒绝逗号小数、单数字小时、越界offset等），使用PostgreSQL微秒精度（解析后截断到微秒、返回UTC）；新值归一化UTC后年份须在0000..9999，避免写入后JSON序列化失败。未改expiry按同一时刻比较，可保留已过期值以编辑其他属性；改成非null值须晚于取得全部编辑锁后的数据库clock_timestamp。过期Key可明确续期或移除期限重新启用；已撤销Key只读，不能恢复。前端保留原始RFC3339微秒，只有用户明确改日期才生成新值，不因datetime-local分钟精度悄悄缩短期限。
7. 锁序tenant SHARE→actor membership SHARE→actor user SHARE→Key UPDATE。锁后重判当前active身份/角色/归属/撤销和CAS。owner可编辑本租户Key，member仅自己，billing_viewer禁止；不存在/不可见/跨租户统一403。GET可见性沿015/016，撤销/过期可读，无需近期再认证；PUT需session/CSRF/MFA策略/近期再认证/ready/Guard/非恢复模式。
8. Key锁内按numeric或big.Int检查非null lifetime_limit≥当前spent+held，不能溢出、重置计数或侵占冻结。低于下限409 key_budget_below_usage，CAS不符409 conflict；当前版本同五元组no-op无审计，旧版本同值仍409。变更与key.configuration.update的before/after五字段及revision审计同事务，无秘密。不提供持久命令回执；未知结果不能自动重发或只凭GET相同判归因。

## 运行与资金

9. 新配置通过原Key锁同步。Reserve锁后读取当前scopes/regions/expiry/limit，预算仍以spent+held+新预占≤limit；RecordAttempt在已有父/Key/渠道/预占锁之后专门读取当前Key的scopes/regions/expiry及来源并复查，覆盖prepared和安全重试，不在共用lockRequest收尾helper中增加拒绝。PrepareDispatch在原授权/渠道/预占/attempt锁后、sending更新前，以新语句数据库时钟再次检查Key有效期和deadline。锁等待跨过到期时刻不得按语句投影中的旧clock放行。
10. 模型/地域/有效期收紧可使尚未sending的调用失败；Reserve前拒绝无资金事实，已预占则按原释放路径归还。预算调低只影响新预占，已冻结金额受第8条保护，旧reservation和安全重试不再预占一次。sending提交后不撤回授权，在途SSE、最终证据、结算/释放/退款/恢复继续按原规则；不在共用完成helper按新scopes/expiry拒绝。
11. 名称变化不改变历史调用/账本或秘密。配置写不重建016桶、不清并发；运行限额、来源和本配置分别CAS。单实例/恢复/不重放、供应商与性能门禁保持。

## 控制台与验收

12. 新增独立基本配置编辑器、configuration lane/unknown Map；三种策略分别保留较高revision，GET/save/list不能回退另外两种配置或用配置快照覆盖当前spent/held。当前actor/tenant/role及弹窗epoch屏蔽晚回；关闭/离页清理输入与秘密，unknown重开仍先核查后显式新编辑。
13. 金额用字符串/BigInt无损转换，0与null区别明确；显示spent+held只是快照，提交仍受锁内最新金额约束。日期保留秒/微秒、显式清空期限；历史scope/region必须可见且未改字段原样发送。保存响应严格匹配冻结五元组及revision（允许服务端规定的规范化和时间微秒）；无法确认则unknown不重试。
14. 专用真实PG随机schema验证触发器/CAS/权限/金额与锁等待/审计回滚/最小角色、三版本互不干扰；billing真实锁等待覆盖三准入边界和到期，已sending收紧后唯一结算/释放/退款/恢复；HTTP六协议拒绝和安全投影；前端金额/日期/旧数据/三策略交错/未知结果。全量测试、strict build、Go race/vet/build、隔离真实main浏览器代表路径、资金只读核对/清理和预览升级后交付。不得使用付费渠道、评审账号/MFA或真实资金。
