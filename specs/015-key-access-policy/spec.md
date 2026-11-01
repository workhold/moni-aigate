# 015：Key 来源访问策略

2026-10-31，状态：限定范围本地验收通过；原生产与性能门禁保留。承接NA-07，按SDD独立实现客户Key来源IP/CIDR限制、展示和安全编辑。动态速率/并发、周期预算、其他Key属性编辑与自助经营仍为后续切片，本轮不改变既有60RPM/burst20和并发16。

## 场景与范围

1. 客户为应用创建受限Key时，可填写最多32个来源IPv4/IPv6地址或CIDR。为空表示不限制来源，兼容原有Key；模型/成员/地域/余额/预算等原有约束继续共同生效。
2. owner可管理本租户的Key，member仅管理自己的Key，billing_viewer无编辑权限。允许已过期Key编辑来源但不改变expiry；已撤销Key不允许编辑或恢复。只替换来源列表，不更换秘密、模型范围、地域、期限或累计额度。
3. 列表显示完整安全策略和字符串版本；打开编辑器固定目标与版本，CAS失败必须重新读取并审核，不能静默覆盖。网络失败或5xx是未知结果，不自动重发；只读刷新可观察当前配置，但没有持久命令回执，不能证明一定由自己的写入产生。

## 输入、来源身份与持久化

4. `allowed_client_cidrs`为字符串数组。最多32个输入项，原始字符串总和最多4096 UTF-8字节；trim后允许纯IP或CIDR，转为masked canonical CIDR、排序去重。拒绝空项、主机名、URL、端口、zone、IPv4-mapped IPv6配置和全网/0，不做DNS。创建时遗漏或null等同空数组；PUT必须明确提供数组，`[]`表示清空，null/遗漏非法。非法值不进入公开错误、日志或秘密字段。
5. 来源由服务端在API Key鉴权时捕获一次：复用014可信代理规则，只有显式受信peer的单一合法X-Real-IP参与；重复/链/非法头退回transport peer，XFF不参与。IPv4-mapped传输地址规范化为IPv4，zone/无效来源视为未知。受限策略遇到未知来源必须拒绝；空策略保持旧行为。客户端不能通过正文/header自报来源快照。
6. `api_keys.allowed_client_cidrs TEXT[] NOT NULL DEFAULT '{}'`及`access_policy_revision BIGINT`由010新增。版本正int64，API用规范十进制字符串；仅规范来源列表实际变化时递增，消费spent/held、读取或其他字段写入不递增。同内容且当前版本为no-op，不新增策略审计；旧CAS即使内容相同也返回冲突。
7. 数据库约束拒绝非canonical/非法/空或NULL成员、过多或过大列表；触发器维护版本，不能直接指定版本绕过。`request_records.client_ip INET`保存服务端来源，历史记录NULL；该字段创建后不可更新，不进入公开请求/账单投影或常规日志。已保存来源用于下一次派发检查，不因代理配置或重启改变。

## API与一致性

8. `POST /api/keys`沿用原字段并增加`allowed_client_cidrs`，严格拒重复/大小写别名/未知字段及尾JSON。创建回包仍只展示一次Key，额外返回规范策略与版本。`GET /api/keys`只返回安全字段及新策略，不返回摘要/秘密/请求来源。
9. `PUT /api/keys/:id/access-policy`接收`{expected_revision:string,allowed_client_cidrs:string[]}`，返回`{id,allowed_client_cidrs,access_policy_revision}`。规范Key UUID/正int64字符串，严格有界JSON，必须满足session/CSRF/MFA策略、近期再认证、ready/Guard及非恢复模式。`GET`同路径返回三个安全字段供编辑/冲突/未知结果核查，不要求近期再认证，权限与列表一致（owner本租户全部、member/billing_viewer仅自己），已撤销/过期仍可读；每次按当前tenant/membership/user有效状态重新判定。
10. 策略编辑事务按tenant SHARE→actor membership SHARE→actor user SHARE→Key UPDATE顺序，锁后重新检查有效状态、owner/member身份、Key所属/撤销/CAS，配置变更与唯一`key.access_policy.update`审计同事务。审计只包含before/after策略与版本；失败回滚不改变Key或资金。

## 生效边界

11. 所有API Key入口（模型目录、Chat/Responses/Messages、Embeddings/Rerank、Gemini）先按当前来源策略筛选。拒绝为403，既有协议错误封装保持，不暴露具体IP或策略；没有上游POST/预占/收费事实。
12. 初筛不代替锁内授权：Reserve在Key锁下检查最新策略；来源快照保存到请求。RecordAttempt在已有Key锁之后复查最新策略；PrepareDispatch在原授权锁序下，用请求保存的来源复查最新策略。排队或安全跨渠道回退时收紧必须阻止尚未获派发准入的尝试。来源为空的历史请求若后来受限，也拒绝新尝试/派发。
13. PrepareDispatch提交`sending`是本轮派发授权同步点；之后获准发送的请求不能被撤回。收紧/清空来源不终止在途SSE，不阻断已有请求的可信量结算、无消费释放、退款、恢复或统计。检查不能放入结算/释放共用锁helper。未发送请求被阻止后按原规则释放冻结，无重复收费或重放。
14. 策略变更不影响Key身份、预算计数、模型/地域授权或财务历史；不增加跳过检查的开关。数据库/权限/来源校验错误均fail closed。

## 控制台与验收边界

15. 创建页有来源列表输入和空列表说明；列表与CAS编辑可查看完整策略。编辑反馈明确即时准入作用与已发请求无法撤回。关闭/切换Key、账号、租户或角色、组件卸载时清理表单和一次性秘密，epoch阻止晚回串线；不自动重放写操作或保存秘密到浏览器持久存储。
16. 用专用PG随机schema验证CIDR规范化/严格字段/CAS/权限/数据库约束、真实锁屏障、各协议和可信代理来源、排队收紧、prepared后收紧、sending后资金收尾及最小角色。前端合同测试/strict build、全Go race/vet/build及隔离浏览器代表性操作后交付。生产供应商/容器/性能与原发布门禁继续独立保留，本轮不调用付费渠道或修改评审账号/真实资金。
