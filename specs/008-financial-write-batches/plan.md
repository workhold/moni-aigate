# 008 实施计划

2026-10-31：功能优先；本计划后移，尚未实施。已校验007摘要并备份before，不代表已开发或验收。

1. 校验007源码与既有v7摘要，固定before普通binary，备份相关源码，独立评审原锁序/时间/提交/错误边界。
2. 提取纯journal构建与账户batch排队/读取helper，维持原postLocked/lockAccounts调用结果。
3. Reserve创建+账户batch及资金尾写batch；finish单资金尾写batch，独立DB clock仍在账户锁之后。
4. 新增真实PG错误注入/取消/新快照与纯构建回归；统一执行专项及全Go race、vet、嵌入构建。
5. 固定after，停止其他本任务构建/测试/profile/浏览器操作，按before1→after1、after2→before2、before3→after3执行三对；保留旧007数据。
6. 公开全部结果及保留决定，同步HLD/LLD和验收，重启本地预览并核对ready及嵌入资源。未达到的正式门槛继续保留。
