# 采购来源内容与真实资金效果校验

2026-11-04；基线 `b3fd5cb6c29367ea65bda92340f51df13ea000c3`。按[PB JSON](../../specs/023-periodic-products/b-procurement-pb-json-contract.md)、[payload关系](../../specs/023-periodic-products/b-procurement-payload-relations-contract.md)和[效果关系](../../specs/023-periodic-products/b-procurement-effect-relations-contract.md)先冻结合同、分工实现，再由根集成。承接[来源值/连续性](procurement-source-values-continuity.md)，实际依赖和未接通部分见[模块细图](../architecture/procurement-replay.md)。

## 实际范围

- [PB两列](../../internal/procurementprefix/source_closure_pb_payloads.go)独立解析四字段输入、十二字段结果与五字段窗口，核原生bool、UTC6/自然日月、NUMERIC100桶、changed/no-op候选、路径/行内时间及八段UTF8长度framing指纹。safe_input没有新增8192字节业务限制；result_snapshot保留8192，二者仍受65536共享字段资源界。旧TEXT actor按原字节参与hash。
- [内容关系](../../internal/procurementprefix/source_closure_payload_relations.go)核四类command快照、typed audit/outbox精确字段与原来源、cause→mutation→link及双向业务槽集合。保留binding快照未投影的历史channel_revision、legacy动态before与not_found后今天已存在等合法集，不能从今天状态补证。
- [效果关系](../../internal/procurementprefix/source_closure_effect_relations.go)核新settlement/journal和HELD/AVAILABLE/有正charge时REVENUE完整line集合，客户tenant/Key及supplier PB真实control/allocation/event、admission/import/catchup和delta。今天余额与累计退款不倒填历史；无marker的旧transition不能因后来配置被强制要求旧PB效果。零delta仍保留真实效果。

上述均为私有纯Go来源叶子，不调用writer或其他域报告构造expected；多值索引只消费唯一原件，按(物理表,自然PK)去重，共享原资源预算/context/绝对deadline，执行错误零结果。局部fixture只验证所列关系，不认证它未补齐的其他叶子或历史观察。

## 独立复核

PB专项与既有JSON/Usage/route交叉race通过（1.911s），包括三份独立固定hash golden、多字节/空TEXT actor、scope/period/state/window组合、MaxInt64 no-op、100位金额、Unicode和取消边界。未引入数据库或writer oracle。

效果关系独立overlay发现：同一新transition业务event_key旁增加另一条全NULL追踪原PE时，正向物理ID关系未发现额外原件。根补event_key多值反向唯一集合及持久回归；原外部反例不改，复验race通过（1.445s）。settlement按request、journal按(tenant,operation)也核真实业务集合，不能用清空pointer冒充旧件。旧closed/后来首配/无absence证明，以及legacy基线后更正至baseline以下的合法集与错误源spent负例，独立race通过（1.522s）。物理同PK重复仍由完整入口的DuplicateRows独立负责。

payload关系13组专项race通过（1.696s）。独立overlay确认并修复三类局部错误：缺link时仍须标记已知成本业务槽的实际额外全NULL outbox；existing/pending非法声明输出须同时记实际原audit/outbox；不同cause的link即使各组唯一，物理PK重复也不能消费。原三反例与四组合法历史场景原样race复验全部PASS（1.539s）。合法集覆盖旧绑定与today配置变化、同attempt后续skip保留已有write输出、existing不认领旧财务输出，以及reused profile block来自另一旧request。

根自审又复现effect重复owner问题：两个不同PE/attempt/root、marker=NULL且无PB event的transition若共用同物理ID，原effect叶子未记pointer（完整入口已有DuplicateRows）。补outcome/transition owner物理唯一门禁，重复source不消费原件，并加入“先唯一合法、再只改重复ID”的持久回归；独立旧源码overlay实证原反例失败。追加审查同样确认first admission与supplier event的直接枚举必须先核物理PK唯一；已补原件/声明者错误归属和new/import/双supplier合法→只改重复ID的持久回归。最终原owner/first-admission/supplier-event三个外部测试全部原样race通过（1.543s）。

## 根集成验证

全部文件冻结后，根执行完整新包：

```sh
env -u TEST_DATABASE_URL go test -race -json ./internal/procurementprefix -count=1
```

**173顶层、905子例全部PASS，0 SKIP/FAIL，3.406s**。旧checker回归 `env -u TEST_DATABASE_URL go test -race ./internal/fundinglotverify -run '^TestCheckProcurementEvidenceGraph' -count=1` PASS，1.573s，旧实现未修改。并行专项不重复加总为集成结果；耗时不作为网关首字节、首token或吞吐基准。

接入新关系后，旧指针测试的exact-slot夹具只有指针、命令及audit内容原为空串，完整入口因此正确报出内容错误。根将它补成独立字面的configure-not_found合法内容与固定摘要，保留原指针叶子与完整入口断言；专项race通过（1.550s），并已进入上面的最终整包结果。未削弱内容检查或原断言。

根完成统一架构语义复核后刷新1778文件快照；只读架构、32表715列/68 generated布局检查与最终 `make check` 全部PASS，包含 `go vet ./...`、Vue strict及Vite构建。前端构建完成后 `go build -trimpath -o bin/moni-aigate ./cmd/moni-aigate` PASS。本段只归档实际结果，行为代码与测试不再修改；归档后再次刷新并只读核验。未配置TEST_DATABASE_URL，不运行需专用数据库的 `make verify-mvp`，不将纯Go或基线CI替代本轮商业数据库验收。

## 新设计与剩余门禁

新增[B-HO1历史观察设计草案](../../specs/023-periodic-products/b-procurement-historical-observation-design.md)：request序和PB scope序共六张候选表，复用现有normal decision前驱和正向PB event桥，补未派生decision的latest/sent、未配置scope以及首次配置完整导入的观察责任。**仅设计，未实现、未安装、未验收。** 全writer/guard、真实锁图、独立reader/replay、ACL/basis/catalog须同版本交付；旧对象不回填可信空序。独立复核补明普通finish没有B-P1 outcome、须另冻受控source，旧scope无head走明确uncovered分支、混合scope分别判定覆盖，以及真实birth/dispatch/recovery/finish入口锁序清单；这些仍为冻结前设计门禁。

来源域继续Complete=false、Qualified为空，无公共Check。完整PB配置/窗口/观察/absence、实际业务cause/资格、逐对象依赖、完整原Base调用和aggregate仍未齐；finish两个历史观察缺口保留。本片不关闭旧十一项Unproven或B-P3整体门禁。

32新表715列/68 generated、40原件482列、旧23数组173字段、原001–022迁移与在线权限不变。本轮没有PostgreSQL商业验收、真实供应商/支付、容器/1Panel或生产性能认证；普通启动不会安装这些候选。README的启动/Compose/1Panel说明已独立只读审查，1158个本地路径链接与README19个标题锚点均有效，未发现部署说明阻塞；这不是实际部署验收。

基线提交 `b3fd5cb` 的[远端CI](https://github.com/workhold/moni-aigate/actions/runs/41682526811)已实际观察10/10任务成功；其结果属于基线，不代替本轮新增代码的本地回归或后续精确提交CI。
