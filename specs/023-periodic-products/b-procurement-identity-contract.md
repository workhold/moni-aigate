# B-P4-D1-I：原采购身份 ABI 的纯 SQL 叶子

2026-11-03，基线 `d93f6096b471bd365bf1708dfbda98b847f5ca7a`。本合同先于叶子实现冻结，作为[写入权限合同](b-procurement-writer-authority-contract.md)与[B-P4设计](b-procurement-prefix-design.md)的必要前置。它仅定义字节转换，不读业务表、不写财务事实、不发行权限，不意味着D1全部完成或闭合writer已实现。

## 1. 问题与范围

现`recordFinalization`对原`FinalizationInput`复制后将ObservedAt设为Go零值，再以`json.Marshal`及SHA-256计算input_digest；`finalizeProcurement`对`{Usage,Reason}`计算语义identity。字段大小写、声明顺序、JSON字符串转义和整数十进制都是持久ABI。新数据库根必须独立计算相同字节，不能接收caller自报digest，也不能以`jsonb::text`的键序/空格代替。

冻结一个独立材料包 `internal/procurementidentity`：仅返回固定SQL模板及版本/SHA，不提供安装器、数据库读取、Go业务marshaller或运行授权。业务源/角色/DDL/newprofile仍由D1其余合同和后续集成确定；默认billing与旧profile不import本包。

## 2. 输入域与错误边界

SQL TEXT输入为PostgreSQL UTF8可表示、无U+0000的文本；BIGINT覆盖完整int64，BOOL为真实布尔。身份函数本身不执行业务UUID、usage、枚举或计价校验。旧操作无法向PostgreSQL TEXT持久化的NUL/非法UTF8不纳入等价承诺；未来业务根须显式拒绝，不能修改为另一个可接受身份。

函数均STRICT、IMMUTABLE、PARALLEL SAFE、SECURITY INVOKER，无SET/动态SQL/业务查询/扩展依赖。任意SQL NULL经STRICT返回NULL；它不是合法身份。原空AttemptID在身份中必须为`""`，物理NULL分支由未来根明确映射为空串再调用，不能直接hash NULL。

ObservedAt不作为函数输入；前像始终固定`"0001-01-01T00:00:00Z"`。原始stage/content先编码，不能先执行sticky归一化。函数不将ID转UUID，不改变大小写或去掉输入前后字节；原uuid.Parse接受集由后续业务输入合同独立保留。

## 3. 六个固定函数

模板标记固定为`{{MONI_SCHEMA}}`，只表示一个安全引用后的受信schema标识符。模板不从运行数据库发现对象，不接收caller SQL。以下名字及参数顺序固定，参数类型使用pg_catalog对应内建类型。

| 函数 | 参数与返回 |
| --- | --- |
| pp_json_string_v1 | `(value text) RETURNS text`；含首尾双引号的Go JSON字符串 |
| pp_usage_json_v1 | `(status text,input_tokens bigint,output_tokens bigint,total_tokens bigint,cached_tokens bigint,reasoning_tokens bigint) RETURNS text` |
| pp_finalization_identity_v1 | 下述17参数，`RETURNS bytea` |
| pp_evidence_identity_v1 | `(status text,input_tokens bigint,output_tokens bigint,total_tokens bigint,cached_tokens bigint,reasoning_tokens bigint,reason text) RETURNS bytea` |
| pp_finalization_digest_v1 | 与finalization identity相同17参数，`RETURNS text` |
| pp_evidence_digest_v1 | 与evidence identity相同7参数，`RETURNS text` |

finalization的17参数依次是：

```text
id, tenant_id, request_id, attempt_id, source, source_event_id,
usage_status, input_tokens, output_tokens, total_tokens, cached_tokens, reasoning_tokens,
protocol_outcome, transport_end_reason, delivery_stage, finish_reason, content_written
```

前六项和usage_status、末四文本为TEXT；五个token为BIGINT；content_written为BOOL。固定模板对六函数均撤销PUBLIC EXECUTE，不授APP、writer或其他角色任何权限；后续安装合同须逐签名授予真正需要的调用角色。本纯包不执行这些DDL。

## 4. 精确前像

usage对象无空白，键顺序与实际`billing.Usage`一致：

```json
{"status":"confirmed_final","input_tokens":1,"output_tokens":2,"total_tokens":3,"cached_tokens":0,"reasoning_tokens":0}
```

finalization对象无空白，键顺序准确为：

```text
ID, TenantID, RequestID, AttemptID, Source, SourceEventID, Usage,
ProtocolOutcome, TransportEndReason, DeliveryStage, FinishReason,
ContentWritten, ObservedAt
```

Usage嵌入上面的完整对象，ContentWritten为未加引号的`true/false`，ObservedAt为固定零时刻字符串。evidence对象准确为`{"Usage":<usage对象>,"Reason":<JSON字符串>}`。最终bytea为这些文本的UTF8字节；digest为`encode(pg_catalog.sha256(bytes),'hex')`，恰64位小写十六进制，不引入pgcrypto。

## 5. Go字符串转义

JSON字符串须与Go默认`json.Marshal(string)`逐字节一致：双引号、反斜杠，短控制转义`\b\f\n\r\t`，其余可表示的ASCII控制符小写`\u00xx`；默认HTML转义`<>&`分别为`\u003c/\u003e/\u0026`；U+2028/U+2029分别为`\u2028/\u2029`。合法其他Unicode保留UTF8，不变成额外surrogate转义。

允许以仅TEXT的`pg_catalog.to_json(value)::text`处理基础JSON字符串，再替换实际`<>&`与U+2028/U+2029；必须通过独立Go/SQL逐字节差分。不可先替换原文本后再to_json而把新增反斜杠二次转义；不能修改原本含字面`\\u003c`等内容。替换中的反斜杠需使用明确字节/转义语义，不依赖caller的standard_conforming_strings设置。

整数使用完整BIGINT规范十进制，无引号、前导加号、空白或指数；包括MinInt64/MaxInt64。负数可在此纯codec转换，但未来业务根继续拒绝不合法负用量；codec不是业务准入器。

## 6. 实现与验收

实现材料仅包含六个函数和相应PUBLIC EXECUTE撤销，所有函数间调用schema限定。模板包公开 `Version = 1`、固定材料SHA与`SQLTemplate() string`；SHA根据经根审阅的静态模板固定，不从live数据库生成expected。

独立测试使用实际`billing.FinalizationInput/Usage`与Go标准`encoding/json`作为旧ABI oracle，不能调用新SQL反推expected。覆盖ASCII、中文、emoji、引号/反斜杠、所有PG可表示ASCII控制、HTML、U+2028/U+2029、空文本与AttemptID、字面反斜杠u序列、bool、完整int64边界以及固定seed的大量Unicode/整数向量。逐字节比较usage/finalization/evidence，另独立比较SHA-256；原始stage/content与ObservedAt零值行为明确回归。

真实PG专项只在指定专库和随机schema执行，以实际受限APP LOGIN调用、无SET ROLE；函数无业务表/财务写权限。检查PUBLIC无EXECUTE、invoker/strict/immutable/parallel-safe和固定六函数，收尾清理schema/角色。显式缺少专库开关须SKIP并记录，不能计PG通过。纯材料与编译/race检查、实际PG差分分别计账。

CI 接线仅在现有 `other` 分区的测试命令环境中显式令 `PROCUREMENT_IDENTITY_DATABASE_URL=$TEST_DATABASE_URL`，复用该job自己的隔离PG16和JSONL证据。分区、现有PB候选开关与测试期限不变，不把该开关提升为全局或启用namespace设计实验、其他C0专项；本机`make verify-mvp`仍需另设此叶子开关才实际执行其PG差分。配置接线本身不是远端测试成功，必须按推送后的准确SHA与JSONL确认。

此叶子成功只解除“DB能独立计算原持久身份”的前置，不解决任何账户准入/审批/成本历史缺证，不改变旧checker、V3读集/roots或正常运行入口。后续完整source/权限/锁图与纯重放合同仍须完成，不能据codec PASS启用候选writer或关闭B-P3。
