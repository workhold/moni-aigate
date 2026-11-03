# B-P3 catalog 静态材料审定记录

2026-11-03。此记录描述静态编制与纯验证；实际 PostgreSQL 双轮安装、权限变异、受保护维护与 APP runtime 验收仍待根集成执行。静态根通过不表示生产迁移、供应商或商业部署已验收。

## 静态输入与独立推导

父材料保持原字节：LegacyV1 的 ordinary/sandbox 两份 JSON 经原 SHA 认证，再施加既有四列 WalletReadV2 ACL 变更，得到父根 `c9466842df4a55490f53b7c0ba9694ee61b5fd5e011912cd2083f2ec191cdf20` 和 `42a63f7178748ea5dd4ce6964cbe9cd77d0131ca8dad795fbd61a4735b206706`。没有复制或改写原 58,388,060 字节材料、历史 roots、compatibility 或 golden。

[固定 delta](../../internal/fundinglotcatalog/expected/periodic-prefix-v3-delta.json) 包含两种 mode、六个 variant 的逐 category 准确 remove/add records。旧记录替换必须同时匹配唯一 identity 与完整原值；新增记录不能覆盖已存在 identity。每个 category 重新按原 canonical identity 排序，全 envelope 的 SHA 必须等于固定 V3 根。未变的原件节点仅以不可变方式共享，改变的数组与 scope maps 单独拥有。

delta 按冻结候选 [DDL](../../internal/periodicbudgetprefixschema/materials/candidate-v1.sql)（`b225c3ff61b735d6830aed1d25edc27bc4784f8e251cb2c1d656196d8d9c630e`）、[ACL](../../internal/periodicbudgetprefixschema/materials/permissions-v1.sql)（`aaea2994d5a79726df8b5c1767b5adb054d14ee8cb81fa12b43b87d24a2f28bf`）和原已认证 PG16 descriptor 模板静态编制。PG16 默认命名截断、表/索引/TOAST/row type/array type、属性/default/generated、55 条约束、FK 内部触发器、函数原正文、依赖闭包及 raw/effective ACL 都有固定记录。无 live metadata、数据库导出、环境变量路径或 caller descriptor 进入 expected 构造。

两个只读 Python 程序和 Go loader 分别验证：

- [check-prefix-profile-delta.py](../../scripts/check-prefix-profile-delta.py)：从原材料、固定 SQL/ACL、受审 PG16 模板与静态语义规则重新编制整份 delta 和六个 tuple，逐字比较已提交材料；不会写文件。
- [check-prefix-profile-roots.py](../../scripts/check-prefix-profile-roots.py)：独立应用准确 remove/add records，重算两份完整 envelope 与六个 schema/roles tuple，并核物理对象数量。
- [prefix_expected.go](../../internal/fundinglotcatalog/prefix_expected.go)：认证固定 delta 字节，校验 DTO 与每次替换、计算完整 envelope SHA、派生 category raw/normalized budgets，并在调用总五秒预算前 eager 初始化。

三个新增内建对象只通过 V3 私有 expected 选择；旧 `collector_builtins.go` 原表未改。来源、精确 OID、PG16 固定源码 SHA 和旧 profile 拒绝行为已冻结于 [B-P3 合同 §4](../../specs/023-periodic-products/b-prefix-profile-contract.md)。

## 数量与权限边界

| application 对象 | 原数量 | 增量 | V3 数量 |
| --- | ---: | ---: | ---: |
| 普通表 | 125 | 3 | 128 |
| 普通表列 | 1145 | 原表3 + 新表25 | 1173 |
| 函数 | 197 | 12 | 209 |
| row/array 等 application 类型 | 252 | 6 | 258 |
| application 索引 | 331 | 12 | 343 |
| 约束 | 1142 | 55 | 1197 |
| 用户触发器 | 255 | 19 | 274 |

55 条新约束是 24 CHECK、3 PK、9 UNIQUE、13 FK、6 `contype=t`；13 FK 另产生52个内部触发器。catalog 闭包还包含新表的6个自动 TOAST relation、3个 TOAST index，故完整 category 增量为 relations21、indexes15、triggers71；这些不应与上表的 application 索引/用户触发器口径混淆。

新表的 SELECT、固定 INSERT/UPDATE 列和12函数 EXECUTE 仅授 APP；projection/worker/peer 没有新增内容权限。两类 generated discriminator 不可直接 INSERT；source 新三列无 APP UPDATE。数组类型的 effective USAGE 按 PG16 element type 权限核验，不能以其 raw 默认 PUBLIC ACL 推断宽授。sandbox 的 peer ACL 依赖文档镜像真实 APP 新依赖，没有授 peer 内容读取。

三个专用发行 API 共用 `proof.go` 唯一 identify→actual LOGIN→collect→identify→opaque proof 生命周期。旧 generic APIs 仍只接受 LegacyV1/WalletReadV2。V3 Installation 共用原16张 C0 输出空集和最终事务绑定复核；三张 PB 业务证据表允许非空。

## 当前验证状态

已通过独立两个 Python 静态重算；`TestPeriodicPrefix*` 与原 `TestWalletProfile*` 纯测试覆盖两种 mode/六个 tuple、全部物理计数、原12函数正文、旧 cache/原件不变、APP/worker/projection/peer 边界、专属 builtin、旧 collector 拒新依赖、完整原值替换、父根/最终根/材料篡改与无效事务拒绝。

尚待实际 PostgreSQL 验收的事项包括 ordinary 五变体和完整 sandbox 各两轮独立名称/OID、旧根拒新布局/新根拒旧布局、非空 PB 历史与原 C0 空集、函数正文/raw/effective ACL/依赖变异，以及根集成的完整受保护 pre/post、observer 和实际 APP runtime。数据库观测只能验证静态根；失败须先解释冻结 SQL 或静态规则的差异，再修正受审材料，禁止把观测结果回填 expected。
