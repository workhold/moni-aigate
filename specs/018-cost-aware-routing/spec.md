# 018 渠道模型采购价与成本感知路由

2026-11-01，设计冻结、实现完成，限定本地验收通过；证据见[018记录](../../docs/verification/cost-aware-routing.md)。承接现有渠道＋精确上游模型成本版本与逐 attempt 成本核算，面向“平台从供应商或另一聚合平台采购后转售”的经营场景。客户售价、客户钱包与采购成本各自独立；018不实现供应商账户余额或累计采购预算。

## 配置与报价

1. 复用完整路由发布和模型 `expected_revision` CAS。不可变 route_policy_versions 增加 `mode=manual|cost_first`、`unknown_cost_policy=allow|deny`；旧路由默认 manual/allow。顶层 API `cost_policy:{mode,unknown_cost_policy}` 与全部 bindings 一次发布。每个 binding 可设 `max_input_cost_rate`、`max_output_cost_rate`，非负 int64 十进制字符串或 null；内部单位为微元 CNY/百万计费 Tokens。null无此维单价上限，"0"要求该维已知免费。策略未提供时采用明确的旧默认，控制台编辑必须携带已读策略，不能隐式重置。
2. cost_first 必须 unknown_cost_policy=deny。manual/allow且无上限保留原行为；manual/deny或任一单价上限为成本管控路由。成本管控时必须有两维均非负、basis为contract/invoice的当前报价；缺报价、basis unknown或缺任一维均排除，不把未知当0。输入计费协议建议显式登记输出价0；其实际输出界为0，不据另一profile限制同名成本配置。仅当前报价参与准入，未来版本未到时不应用；上游模型按绑定的精确名称匹配。
3. 候选读取时冻结 cost_version_id、input_rate、output_rate、basis。当前报价按 effective_at DESC、created_at DESC、id DESC确定选择，兼容历史同刻多版本而不改历史。新发布相同 channel/model/effective_at 拒绝。有效时刻允许登记已发生合同/未来调价，影响后续尝试；已有attempt不能追溯改价，差异走成本更正。
4. 成本发布要求 `expected_channel_revision` 正int64字符串，与channels.management_revision比较；并持有channel UPDATE锁，成本版本＋审计同事务。数据库INSERT触发器让直接SQL也参与同一channel同步，递增management_revision并阻止新重复时刻，不依赖所有调用者自觉加锁。API金额仅规范十进制字符串或null；严格JSON拒绝重复/未知/别名/尾JSON，安全元数据返回费率字符串。写入结果不明不能自动重放，界面先重新读取报价与渠道revision再显式新编辑；GET相同不证明原请求归因。

## 选择与派发

5. 权限、地域、能力、启停/隔离/撤销、已尝试渠道、健康与两层容量先过滤；采购未知/上限也是硬过滤。再取最高priority层；cost_first在该层比较 `S=input_upper_bound*input_rate+output_limit*output_rate`，使用big.Int无损计算，不提前舍入。S相同时按原渠道weight，再按凭据成员weight选择。池大小不增加渠道概率；manual沿原排序。跨全部渠道比价应将priority设相同。
6. S是当前请求保守用量上界对应的采购比较分数，不是实际账单、采购余额预占或一定最低的实际成本；最终cost仍按可信最终usage和attempt冻结费率核算。不同渠道token语义和供应商硬界须先通过现有profile认证；不因报价便宜跳过供应商计量合同。没有供应商自动查价、汇率、缓存折扣或阶梯价。
7. RecordAttempt在原tenant→membership→Key→model→channel→reservation锁后，读取该request冻结route_revision的策略与binding上限，重新读取当前报价。成本管控路由必须收到候选报价，完整匹配当前版本/两维价格/basis并重验硬限制，冻结同一报价；不存在合法quote拒绝。兼容的manual/allow无上限内部调用可不提供quote，沿原规则在attempt创建时冻结最新成本，不引入新的成本拒绝。
8. PrepareDispatch在全部授权/渠道/凭据/reservation/attempt锁取得后，以新数据库时钟重新选择当前报价并与attempt快照比较及重验策略，然后才标记sending。覆盖并发调价、锁等待和无写入的定时生效；一致性保证以此次锁内报价检查为同步点。quote变化返回503 cost_quote_stale，禁止网络发送、自动重新报价或自动重规划，释放客户冻结；明确未消费前已有attempt仍按原not_sent证据收尾。
9. sending获准后调价不撤回调用；已发请求的证据、结算、释放、退款、恢复仍使用原合同。不得在共用财务收尾锁helper增加新成本策略门禁。报价门禁约束本库登记价格，无法强制供应商外部真实扣款。两次跨渠道安全尝试及SSE提交后不重放规则不变，每次attempt各记成本而客户仅冻结一次。
10. 平台probe与目录发现不使用客户cost_first偏好或客户报价门禁：分别沿独立平台探测预算与只读发现合同，不悄悄增加调用。新增排除原因限白名单（cost_unknown、cost_limit_exceeded、higher_cost）；仅平台管理员可读成本、完整路由与attempt解释，客户接口不泄露采购价/秘密/出口地址。

## 控制台与验收

11. 路由编辑显示当前策略、未知处理与逐binding两维采购单价上限；单位为CNY/百万Tokens，金额用字符串/BigInt无损转换。策略和全部bindings完整发布；冲突必须重读审核，未知写结果禁止自动重发。关闭/切页/身份变化后旧响应不可覆盖新编辑，保留不明结果提示。
12. 成本版本编辑固定选中渠道revision，提交CAS；冲突重读后重新编辑，未知结果明确核查。现有历史版本只读及成本更正保留。新增能力解释中明确最高优先级内比价、未知排除、预算与实际账单边界。
13. 验收：路由版本CAS/不可变性、未知/0/上限边界、精确大金额分数/同价权重/池大小、健康与容量降级、两次安全尝试；真实PG覆盖直接INSERT同步、同刻重复/旧重复确定性、调价与未来时刻及全部锁等待、sending后唯一收尾；HTTP六协议与权限/严格字段；前端编辑/冲突/不明结果/晚回；全量race、strict build及隔离真实main浏览器代表经营链路。供应商、容器/1Panel、灾备、许可证与性能发布门禁保持独立。
