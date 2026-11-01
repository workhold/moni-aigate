# 003 验收矩阵

实际执行与未覆盖项写入 `docs/verification/native-protocols.md`，不以测试数量替代场景。

| AC | 场景与判据 |
|---|---|
| NP-AC01 | 空协议旧 Chat 正常；非法协议/参数创建失败；错端点、媒体/托管工具/有状态参数发送前拒绝，预占/上游POST均0。 |
| NP-AC02 | 三协议真实HTTP非流与SSE到最终量、结算、账单；供应商认证正确，公开模型替换，客户凭据不外泄。 |
| NP-AC03 | Messages Bearer/x-api-key 单独有效；双header/重复header拒绝；Responses/Chat不接受x-api-key代替Bearer。 |
| NP-AC04 | Responses completed/incomplete，Messages message_start/delta/stop；SSE按任意字节边界解析；没有Chat DONE；成功终态到客户端时数据库已settled。 |
| NP-AC05 | 原生工具仅转发；tool-only输出正常；缓存/推理子集不重计，Anthropic cache creation/read不漏计。 |
| NP-AC06 | usage缺失/null/冲突、error、裸EOF不发成功终态，不凭估算扣款；取消关闭上游，许可归还，未知消费不自动重放。 |
| NP-AC07 | 供应商错误/任意额外字段秘密标记不泄漏；SSRF、大小/时限和原有资金/撤权回归通过。 |
| NP-AC08 | 原生渠道probe以相应协议派发、持久独立结果，不触碰客户钱包。 |
| NP-AC09 | Playground协议选择、文本/流/取消/request_id/usage/error可用；Key不落持久存储或请求URL。严格TS、前端测试与构建通过。 |
