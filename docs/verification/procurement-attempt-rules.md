# 原 attempt 规则与报价的独立差分

2026-11-04；基线 `1d457262bfd76832dba4175d93fed612e8c2a03b`。先冻结[A0 合同](../../specs/023-periodic-products/b-procurement-attempt-rules-contract.md)，再实现[五函数材料](../../internal/procurementattemptrules/materials/attempt-rules-v1.sql)与[独立 billing 差分](../../internal/billing/procurement_attempt_rules_postgres_test.go)。本片没有修改 billing 业务 API、既有六函数 identity、账户 v1、001–022 或线上权限。

## 实际验证

新包静态 `env -u TEST_DATABASE_URL go test -race ./internal/procurementattemptrules -count=1`：3 顶层 PASS，1.732s。固定字节、inventory 与属性检查只证明材料边界，功能正确性另由真实数据库差分验证。

专库采用 PG16、`127.0.0.1:55439/moni_test` 与安装 LOGIN `moni_test`，每个顶层独占随机 schema 与受限 actual APP LOGIN，不使用 SET ROLE。APP 仅获五函数、原 `pp_json_string_v1` 和报价八列 SELECT，PUBLIC 未授权及 APP 无 DML 均实际探测；本地连接为无密码专用配置，不据此宣称密码认证验收。

```sh
env -u TEST_DATABASE_URL \
  PROCUREMENT_ATTEMPT_RULES_DATABASE_URL="$PRIVATE_TEST_DATABASE_URL" \
  go test -race -p=1 -json ./internal/billing \
  -run '^TestPostgresProcurementAttempt(OriginalRules|QuoteSampling)$' \
  -count=1 -timeout=180s
```

实际命令另由 `caffeinate -di` 保持运行；原 statement_timeout=5s、lock_timeout=3s 未放宽。最终 **2 顶层 / 224 子项全部 PASS，0 SKIP / FAIL，2.598s**。显式无库编译曾返回 PASS 但两个顶层均 SKIP，未计入数据库证据。CI billing 分区新增专库 opt-in，推送后的对应提交另核远端结果。

覆盖四种 UUID 形式、38 字节非括号包裹、ASCII 控制与非法多字节；原文字去重和 selected 匹配；20 reasons、空/1000/1001 项、int64 两端、uint64 上半区和最大值、非法结构/类型/小数/NULL、Go 转义及逐字节输出；expected 空/NUL/非法 UTF8/大 BYTEA、nullable/负 rate 与实际 Equal 的 true/false；无价和 NULL selector 一行真实时钟、部分未知价、0 价、未来价不选、equal-effective 的 created_at / id 稳定选择。合法 typed 路由事实上无法达到 262144 字节，未伪造不满足 UUID/reason/1000 项的正例声称覆盖该边界。

原私有 Go 规则直接作 oracle，不复制 validator 自证。真实 original22 的 cost_versions 与原查询作选价对照；同生效时刻旧报价在原 012 允许时生成后升级到 022，原 013 guard 全程保留。

## 失败与修正

首轮测试准备 channel/version 时漏掉原 008 要求的 frozen credential membership，两个顶层均在 COMMIT 以原 23503 失败，没有规则差分证据。补齐真实成员后，第二轮原规则通过，但选价 fixture 尝试在 022 下新增同生效时刻报价，被原 013 的 23505 正确拒绝。测试改为在真实 012→013 历史边界前准备合法旧数据，业务 SQL、原约束与新材料均未更改。

此后 2 顶层 / 218 子项 PASS；独立审查又提出显式 NULL/非法联合的覆盖缺口，补入后得到上述最终 224 子项。之前失败和中间通过不与最终结果重复计数。

| 忽略的本地原件 | 字节 | SHA-256 |
| --- | ---: | --- |
| `.local/moni-attempt-rules-initial.json`，首轮 FAIL | 3270 | `ee6e605b86095c039c1dae44511dc4676a34234fa89d0c076f69a587c99c3f5e` |
| `.local/moni-attempt-rules-reviewed.json`，第二轮 FAIL | 242358 | `c67a78af315a7c95d45463e09319b662d38339d8e2bc71880f12437530fed3c3` |
| `.local/moni-attempt-rules-reviewed-final.json` | 252482 | `239d4ee05a1f689f44dc80e936d8a0791101bf8ea223db0af781147da917f1e7` |

## 交付边界

A0 已实现并完成上述独立范围；完整 `pp_record_attempt`、完整 A1 矩阵、A2/A3 采购效果与 command+reserve、全域 exact-link、完整 ACL/catalog/profile 和 R2 仍分阶段交付，不由本页验收。A1 真实 request birth 及最小 exact-link 已实施，其实际窄范围 PG 证据由[A1 续片](procurement-request-birth.md)独立记账。

README 新增 OpenAI Python SDK 接入示例，保留网关地址包含 `/v1`、逻辑模型名及客户端未知消费不自动重试的配置；首页特点、启动和 Compose/1Panel 说明继续按实现边界复核。本轮没有新增可售模型、默认账号、正式收款或生产部署能力。系统模块、数据与权限依赖见[架构细图](../architecture/procurement-attempt-birth.md)。
