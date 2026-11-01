# 023 集成人设计复核

2026-11-01。以下保留最初设计裁决；**A0现已冻结，A完成[限定本地验收](../../docs/verification/tenant-pricing.md)，B0/C0仍未冻结**。C必须先通过022-C全资金来源验收。

## 已接受的方向

- A采用单tenant单售价组、绝对输入/输出价；缺组价拒绝；退休只停止新分配与新发布。匿名目录只显示公共默认价，客户看本租户报价；Reserve在授权/model/group锁后、钱包等待前冻结价格。
- B采用UTC自然日/月、原准入期归属、跨期held作为carry；首次从下一完整周期启用，已关闭scope继续跟踪，关开不重置。客户消费退款不恢复周期spent；客户与采购预算独立，累计限额和周期限额同时满足。
- C先固定期限和显式续期，自动续费另片；本次不接受缺少来源分录、退款比例或到期桶定义的购买实现。

## A0编码前必须闭合

1. 对照当前全部实际读取点：`internal/billing/reserve.go`、`internal/catalog/public_catalog.go`、`internal/catalog/store.go`、`internal/server/queries.go`。尤其旧`/api/prices`历史列表不能变成全组价格枚举，旧默认发布要显式固定NULL域；不能仅改Reserve。
2. 追加DDL必须列出当前唯一约束名、`NULLS NOT DISTINCT`或等价域内唯一机制、不可变价格guard兼容、tenant assignment审计与新快照完整性。旧请求保持legacy证据边界，不能将NULL组误当“已证明当时默认价”。
3. 合并现有所有model/group/tenant锁与FK隐式锁，列发布、退休、调组和Reserve完整图；禁止管理路径group→tenant与调用路径tenant→group形成逆向。退休后既有客户继续，须PG锁等待反例证明。
4. 冻结缺价预览的范围、字段、分页及当前权限；预览不是后续请求准入承诺。单SQL客户报价在当前tenant/模型权限范围内，跨tenant资源不泄漏组。
5. 精确管理命令表、target绑定、回执保存result快照、COMMIT未知核查路径；只有操作metadata而未保存result不足以重读原结果。列表末页当前草案为空字符串；A0应统一明确最终合同，HTTP/UI不得各用空串与null。
6. 原协议报价失败码已有`price_unavailable`的HTTP映射；草案写402而当前billingError未明确该码，需要统一兼容与推理协议裁决后冻结，不能由实现者偶然选择。

## B0编码前必须闭合

必须用实际Reserve/PrepareDispatch/finish/采购证据/更正/恢复代码冻结helper与全部锁层；首次配置legacy held枚举必须与并发创建和收尾互斥。追加事件与投影同tx、完整DFK/guard/最小角色和NUMERIC累计边界均需明确。周期归属的最后DB时刻晚于价格观察可以成立，两者不可混用。时钟回退只允许fail closed及显式核查，无reset入口。

固定当前窗口查询、allocation精确state、平台/租户操作查询路径、独立有界限流、CAS及所有异常代码后，才能分别交给客户与采购负责人编码。最终交叉测试同时覆盖调价、月界、锁等待、24小时客户释放、采购unknown和旧备份恢复。

## 证据与统一架构

本文保留设计复核依据；当前A0已冻结、A通过限定本地验收，B/C保持planned。代码、测试、设计稳定汇总后由集成人更新统一架构正文与内容快照，不能单独为本文件盲刷清单。


## A0根最终裁决

2026-11-01，已接受并冻结[a-contract](a-contract.md)，包括保留price_unavailable HTTP503、原回执target/input_fingerprint及不可变result、安全单SQL preview、客户历史仅当前域已生效价、独立有界准入。真实PG原型识别并修正CASE括号错误，NO KEY UPDATE避免登录隐式FK反锁；其后018/helper/Reserve/HTTP/UI已实现并通过限定本地验收。原型与真实运行证据分别记录，B0/C0仍未冻结。
