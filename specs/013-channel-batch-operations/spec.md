# 013：跨渠道批量启停与回执

2026-10-31，沿NA-06/A3继续，状态：限定范围本地验收完成，009迁移/最小角色及最终预览就绪，资源匹配；直接证据见[验收记录](../../docs/verification/channel-operations.md)。功能优先，性能专项后移。

## 用户出口

管理员在渠道列表勾选当前页渠道，批量启用或停用；预检逐条展示状态、阻断原因及已有/旧版本发布绑定数量。确认后原子提交，任一阻断则整批不变更。每次提交有稳定operation_id和不可变回执，断网后可查询同ID，不凭未知响应重复生成新操作。

本切片只操作`channels.enabled`。不修改版本/凭据池、撤销状态、quarantined、模型绑定/revision、客户资金或授权；不访问供应商、不调用付费probe/目录GET、不自动发布。启停是所有历史发布绑定的实时渠道门禁，成功启用可能恢复原有路由服务；它不是012的新池发布。

## 状态与数据库

1. 新009迁移增加`channels.management_revision BIGINT NOT NULL DEFAULT 1 CHECK(>0)`。数据库BEFORE UPDATE无条件写old+1，INSERT强制为1，调用者不能回写或伪造；任何渠道UPDATE均递增，防版本/状态ABA。它不是模型route revision。渠道列表以十进制字符串返回，前后端保持int64精度。
2. 新不可变`channel_batch_operations`整行保存operation ID、actor、action、输入摘要、applied/rejected、目标/变更计数、受限JSONB逐项快照和时间。单行包含完整items，不存在提交后追加子项的问题；应用可SELECT/INSERT，无UPDATE/DELETE，owner普通SQL也不能改，projection无权限。1..100目标、1..100回执项、256KiB结果上限；数据库约束验证状态/计数、items长度与changed条目数。无秘密/URL/原始错误/请求正文。
3. 目标固定`channel_id,expected_version,expected_revision`；ID为规范UUID，revision为1..MaxInt64规范十进制字符串。拒空集合、超100、重复渠道、不合法action/ID/revision；按channel ID排序规范化，次序变化不影响摘要。每次新的操作生成新UUID；同operation ID仅允许同actor及同规范输入重放。

## API与事务

4. `POST /api/platform/channel-operations/preflight`接收`{action:"enable"|"disable",targets:[...]}`，只读单SQL一致快照，不持锁、不审计、不产生回执、网络或资金事实。返回`action,can_apply,changed_count,items`。items包含channel_id/expected_version/expected_revision、before状态、live_credentials、published_bindings、older_version_bindings、status与reason。未知渠道before=null。状态为ready/unchanged/blocked；任一blocked则can_apply=false、changed_count=0。预检不是承诺或锁，apply必须重检。
5. `POST /api/platform/channel-operations`额外接收operation_id。事务先按operation ID取得advisory xact锁，再用新语句查回执；已有同actor+digest直接返回原回执，不重新验证或写渠道。异内容/actor复用409。首次执行按channel ID顺序锁全部存在渠道FOR UPDATE，再锁当前池所有credential ID FOR SHARE（全局ID顺序），再用独立新语句读取当前状态/撤销信息。不得反向锁model；发布现有model→channel锁序保持。
6. 每项expected_version及expected_revision都须匹配。disable不要求live凭据且不清隔离；enable拒绝quarantined或当前池无未撤销凭据，已有enabled也不冒称条件有效。published/older_version绑定数量仅作提示，不声称所有模型可调用、全池认证或实时容量可用；启用仍受旧路由版本、成员撤销、binding隔离、模型/客户授权和健康容量限制。
7. 任一blocked：不更新任何渠道，ready项改not_applied，保存rejected回执及`channel.batch.reject`审计；全部通过：只UPDATE确有enabled变化的渠道，记录实际递增revision与before/after，其他项unchanged，保存applied回执与`channel.batch.apply`审计。业务变更、完整回执与唯一审计同事务；SQL/审计失败全部回滚，不返回伪成功。applied可以changed_count=0，不为无变化项制造revision。
8. HTTP200表示已获得持久回执，界面必须读取其status，不能把rejected当成功。返回`operation_id,actor,action,status,target_count,changed_count,created_at,items`；items结果status为changed/unchanged/blocked/not_applied。before/after仅固定current_version/management_revision/enabled/quarantined，预检指标单列。原子拒绝后同ID重放永远返回原拒绝，重新审核才生成新ID。
9. `GET /api/platform/channel-operations`使用原有50条分页，只返回摘要；`GET .../:operationID`返回完整原回执，不将后来渠道状态覆盖历史。均仅admin；POST继续session/CSRF、近期再认证、可选MFA、Guard/ready和恢复门禁。GET404不证明另一未结束事务没有提交；未知结果只查询同ID，未确定结果时不自动创建新ID重试。

## 控制台与边界

10. 仅admin渠道页有勾选列和批量启停/回执入口。最多当前页50项，不能跨页累计；筛选、刷新、分页、切resource清选择并失效预检；列表请求加epoch，晚回不能把旧列表覆盖新页。弹窗冻结选中目标、action和预检，提交前用户可逐项检查。启用明确提示可能恢复旧发布路由；停用只拦截后续准入，已发上游无法撤回。
11. 确认提交时生成一次operation ID，冻结命令。网络/5xx/响应不明时显示待确认并保留ID，提供只读查询；关闭或切页不能丢失已提交ID，历史入口可找回。正常只内存保留安全ID，不存秘密；可复制ID供后续输入查询。回执清楚区分整批应用、整批拒绝及结果待确认，历史查看不自动执行POST。异步操作用各自epoch隔离。
12. 本轮没有跨渠道密钥导入/标签体系、批量模型/路由发布、批量付费探测、自动轮转、后台队列/定时同步或供应商余额；原生产/供应商/性能门禁保持。测试只用专用PG隔离schema/角色与本机HTTP，无真实客户资金或评审账号/MFA变更。

## 验收

真实PG验证单调revision/防伪/ABA、预检零写与快照、全成功/混合拒绝/缺失/no-op、重排与并发同ID重放/冲突、排序锁/锁后新快照、credential撤销屏障、审计失败回滚与单连接资源清理、最小角色及历史不可变。真实客户HTTP验证批量停用后不POST、不收费，重新启用恢复同一旧route/pool并唯一收费；旧准入attempt派发屏障仍有效。前端验证选择/晚回/回执/未知结果与ID生命周期，strict构建；最终全Go race/vet/build、009迁移/角色及预览资源匹配分别记录。
