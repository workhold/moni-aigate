# 023-B 独立 Reader 冻结切片

2026-11-01，根冻结[b-http-contract](b-http-contract.md)§2当前读权、§3 Reader接口/只读事务、§6分页及既有§1/§7 DTO/cursor作为**独立Reader实现与隔离PG测试**；不冻结Commander/HTTP/UI，不追加迁移或接main。与[Tx切片](b-tx-contract.md)不同文件并行，SQL仍读三份设计fixture。没有从普通网关调用，不将模块测试记作产品预算上线。

精确构造器/全部Reader方法沿HTTP§3。私有pool与唯一CursorCodec；nil pool或非32byte Pepper构造失败；不读env或连接DB确认授权，所有授权在每次方法的RR只读事务。方法nil/零Reader返回安全unavailable。parent context与5秒取更短，设置事务内3秒lock_timeout/5秒statement_timeout，关闭Rows/只读COMMIT确认后返回完整DTO；回滚独立≤5秒，不用失败request context。无重试、写入、网络、pb_observe、Key锁或业务行锁。

平台先验证实际active user/admin或两finance角色，再查询目标tenant/account存在，允许暂停目标。钱包/Key先验证当前tenant/user/membership均active：wallet仅owner/billing_viewer；Key为owner/member/billing_viewer，owner读全部本tenant Key，其余仅自己Key，revoked历史仍可读。身份失权403，合法但不可见或不存在目标404。operation按入口限定scope，tenant operation只允许本tenant的Key kind，再重验当前Key读权；不向失权身份返回原operation信息。所有ID先规范校验400。

当前view在一个SQL用`WITH instant AS MATERIALIZED (SELECT pb_now() AS at)`取得唯一as_of，再按受信scope查control并调用pb_view_at；未配置返回精确unconfigured而非建表。所有JSONB snapshot使用精确字段集合解码并Validate（含嵌套window）；operation元数据与result.Validate和完整目标关系一致。历史列表先授权、按可信scope解析control，再比较cursor绑定；坏cursor即使无control仍拒。固定51，全部验证，输出前50，cursor来自第50条；SQL明确COLLATE C，空页非nil集合/空串。列表不读取当前view或做clock高水位检查，不JOIN全来源表，不做全control SUM。

只读SQL/池/ctx/COMMIT失败为periodic_budget_unavailable；实际23514精确clock regression为periodic_clock_regression；已确认的投影/DTO/约束异常为periodic_budget_invariant；其余错误均固定code，Error()不含原PG正文。当前权限错误使用已有forbidden/not_found；cursor使用唯一纯codec。Reader不声称HTTP admission或写门禁已实施。

测试至少真实001–019+三SQL+受限APP LOGIN的当前权限/跨tenant/原operation/无写、分页0/50/51/同time C顺序与错误scope cursor、当前view clock回退但历史仍读、DTO坏投影拒绝及精确资源清理。大样本EXPLAIN与完整撤权并发/HTTP/main继续列作集成门禁，不能把单包验证扩称完整商业验收。
