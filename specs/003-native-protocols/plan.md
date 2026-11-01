# 003 实施计划

1. T-01：provider 独立协议 parser、上游认证与安全网络、JSON/SSE 原生响应/usage；真实本地 HTTP fixture 验证。
2. T-02：catalog 模型协议、三个推理端点共用商业管线、原生终态在结算后输出、Messages 客户认证与错误形状、协议 probe。
3. T-03：控制台模型协议、Playground、接入示例与独立流解析测试。
4. T-04：真实 PostgreSQL + HTTP 纵向验收；全量回归、构建与浏览器检查；更新HLD/LLD/路线图与实际执行记录。

并行任务边界：provider、web、billing 性能优化分别由独立代理实施；server/catalog/spec 与集成验证由主代理负责。billing 优化不得改变派发状态机、锁顺序、资金约束或证据完整性。
