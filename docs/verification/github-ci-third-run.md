# GitHub 第三轮 CI：十组完整通过

2026-11-02，[运行41342022443](https://github.com/workhold/moni-aigate/actions/runs/41342022443)，精确源码 `f64b56feba984ceed4912c5cf06accf22b0aac88`。十个job全部success；[独立机器核验](github-ci-third-run.json)保存运行身份、各组计数、分区关系、实际终态与跳过原因。完整原日志SHA-256为 `938c5cdaa921cd385ce8472ce5090e9109886490ace3a0ee720d1e4bdf59e446`。这份证据只对应该SHA，不自动覆盖后续T2新增代码。

42个包在billing、identity、server、other中的归属为1/1/1/39，互斥穷尽。billing实际157个顶层、server205个顶层，各自动态四分片无重漏，所有启动父/子用例均有唯一终态；identity57个顶层通过。整轮1036顶层PASS、54顶层SKIP；计父子为2977 PASS、92 SKIP，零FAIL。billing helper和server容量opt-in各有合法SKIP；other包含未开启的专用C0、真实main等opt-in，不能写成全部测试均PASS。

前端286项全部通过，无失败或跳过；架构只读检查、vet、嵌入构建均success。十组PostgreSQL readiness均实际核对Asia/Shanghai；产品SQL、冻结inventory与业务期限保持原合同。

此轮验证了CI分区和临时PG环境修订，未运行专用C0 catalog/maintenance/candidate及真实main/capacity验收，也不代表正式供应商、正式支付或生产部署通过。专库专项继续以各自独立验证记录为准。
