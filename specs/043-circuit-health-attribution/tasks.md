# 043 任务与验收状态

2026-11-26。范围见 [spec](spec.md) 与 [design](design.md)；实现基于 main `fb2146f6`。

| 任务 | 状态 |
|---|---|
| 修正案规格与实施设计 | 已完成（本目录） |
| HLD §凭据池段落、LLD §8 修正案段落 | 已完成 |
| `provider`：`ClassOverloaded`（529）分类 | 已完成，分类测试通过 |
| `routing`：归因账本/成功近因/`Overloaded` 短休/凭据冷却 | 已完成，单元测试（含 race）通过 |
| `server`：outcome/detail 映射、凭据级 429 冷却接线 | 已完成 |
| 故障注入矩阵（专用 PG，随机 schema） | 进行中 |
| 相关包 `-race`、`make check` | 待最终提交后执行 |
| 统一架构正文/changes/manifest 刷新 | 待专项通过后执行 |
| 完整 serial-v1 029（干净最终提交） | 待全部上述完成后执行 |
| 独立 PR（不自动合入） | 待 029 通过后创建 |

## 验收边界

- 本片只改变健康动作与 rest；`SafeToRetry`/`Consumption`/attempt 预算/首事件边界/唯一结算/对外错误码不变，回归用例必须保持通过。
- 归因窗口（60s）、成功近因（5min）、过载短休（30s）为固定常量，不进入路由版本参数。
- mock/专项不认证真实供应商；最终完整 029 才是共享商业链路验收。
