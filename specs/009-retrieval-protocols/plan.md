# 009 实施计划

1. 固定输入协议和input-only计费合同，明确Embeddings与Jina Rerank范围。
2. 并行实现Provider契约/fixture与控制台/Playground；主任务实现catalog/Reserve/路由/probe与真实PG纵向验收。
3. 集成校准边界，运行针对性回归，再执行全Go race、前端测试/strict build、vet与嵌入构建。
4. 只用本地mock核验最终预览，更新功能矩阵、SDD验收与后续能力顺序；不再运行性能专项。
