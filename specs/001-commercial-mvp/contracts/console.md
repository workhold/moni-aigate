# 控制台接口合同

状态：实施合同；2026-10-30。关联 FR-01/02/04/05/08/10，NFR-02。Vue 3/TypeScript 控制台独立实现，不使用上游 UI 或素材。

## 传输与身份

- 所有接口前缀 `/api`，同源 Cookie 会话，`credentials: include`。
- 成功 `{data, request_id, next_cursor?}`，列表的 data 是数组；错误 `{error:{code,message},request_id}`。
- 写请求带 `X-CSRF-Token`，取自 `/api/me`。资金制单/审批附带稳定 `Idempotency-Key`。
- 金额均为微元十进制字符串；前端仅用 BigInt 换算 CNY，不用 Number 运算资金。
- 时间 ISO-8601 UTC，界面按浏览器时区显示。未知用量/成本显示“待核对”，不得显示为 0。
- 平台角色与租户角色独立；隐藏不具备能力的操作不代替服务端授权。

## 身份与安全

| 接口 | 请求 | data |
|---|---|---|
| GET /me | — | `{user:{id,email,platform_role,mfa_enabled}, tenant:{id,name,role}|null, tenants:[{id,name,role}], csrf_token, reauthenticated_until?}` |
| POST /auth/login | `{email,password}` | `{mfa_required?,enrollment_required?}`；随后刷新 me |
| POST /auth/logout | `{}` | 任意成功值 |
| POST /auth/mfa/setup | `{}` | `{secret,otpauth_url,recovery_codes?}` |
| POST /auth/mfa/enable | `{code}` | 任意成功值 |
| POST /auth/mfa/verify | `{code}` | 任意成功值 |
| POST /auth/reauth | `{password,code?}` | 任意成功值 |
| POST /auth/password | `{current_password,new_password,code?}` | `{reauthenticate:true}`；重新验证当前密码和已启用MFA，成功撤销全部会话并清Cookie，前端清状态后跳转登录 |
| POST /tenant/switch | `{tenant_id}` | 任意成功值；随后刷新 me |

`enrollment_required`仅在`MFA_ENFORCE_PLATFORM=true`且未绑定平台/财务账号时为true，并优先引导安全页；默认false时未绑定账号可直接进入工作台，安全页说明MFA可选并提供返回工作台入口。已绑定账号的`mfa_required`仍显示验证码表单，不随配置关闭而跳过。服务端返回 `reauth_required` 时打开再认证对话框，通过后由用户重试原操作，避免自动重放资金写入。密码表单核对重复输入并按 UTF-8 12–72 字节验证，离开页面或成功后清除所有密码和验证码。

MFA 绑定页明确说明动态验证码由 Google Authenticator、Microsoft Authenticator 等 TOTP 应用每 30 秒生成，不是短信验证码；网页不生成用于登录的动态码。二维码仅在浏览器本地从 `otpauth_url` 生成，不向第三方二维码服务发送密钥，手动密钥与 URI 为扫码失败的备用方式。恢复码与 6 位动态码分别说明，确认保存恢复码并输入 6 位数字后才能提交。未启用 MFA 的待绑定阶段可再次调用 setup 重新生成；提交前确认旧待绑定密钥、二维码和恢复码将失效，收到新绑定信息后清空旧验证码和保存确认。成功启用或离开页面清除二维码、绑定信息和输入值。MFA默认可选，`MFA_ENFORCE_PLATFORM=true`时才强制未绑定的平台/财务账号完成设置；已启用账号仍须MFA验证，后端近期再认证约束保持不变。

## 客户数据

| 接口 | 请求/字段 |
|---|---|
| GET /wallet | `{available,held,total_credited?,total_charged?,total_refunded?}` |
| GET /reports/summary | owner/billing_viewer：`{tenant_id,credited,charged,refunded,released,requests,projection_pending,as_of}`，服务端从会话确定租户，不提供供应商成本 |
| GET /models | 数组 `{id,public_name,enabled?,context_limit,output_limit,default_output_limit?,reservation_strategy?,input_rate?,output_rate?}` |
| GET /prices | 数组 `{id,model_id,model_name?,input_rate,output_rate,effective_at}` |
| GET /keys | 数组 `{id,name,prefix,scopes:string[],lifetime_limit?,spent,held,expires_at?,revoked_at?,created_at?}` |
| POST /keys | `{name,scopes:string[],lifetime_limit:string|null,expires_at:string|null}` -> `{key|secret, ...元数据}`，明文只显示一次 |
| DELETE /keys/{id} | 撤销，不删除资金历史 |
| GET/POST /members | owner读取本租户成员；创建请求 `{email,password,role:"member"|"billing_viewer",allow_all_models,model_ids}`，仅委派自身有效授权 |
| PATCH /members/{user_id} | owner修改成员 `{role,status,allow_all_models,model_ids}`，不能自降权/自停用；所有者增设由平台处理 |
| GET /requests?cursor= | 数组 `{id,model_name?,model_id,status,billing_status,charged_amount?,reserved_amount?,input_tokens?,output_tokens?,created_at,settlement_id?}` |
| GET /requests/{id} | 请求标准字段及安全明细，不显示渠道秘密/原始正文 |
| GET /journals?cursor= | 数组 `{id,kind,amount?,reference?,created_at}` |

## 平台与财务

| 接口 | 请求/字段 |
|---|---|
| GET /platform/tenants | 数组 `{id,name,status,owner_email?}` |
| POST /platform/tenants | `{name,email,password}`，邀请创建 owner，初始密码一次安全交付 |
| PATCH /platform/tenants/{id} | `{status,model_ids?}`；grant空数组明确拒绝 |
| GET /platform/tenants/{id}/members | 数组 `{user_id,email,role,status,model_ids?}` |
| POST /platform/tenants/{id}/members | `{email,password,role,model_ids:string[]}` |
| PATCH /platform/tenants/{id}/members/{user_id} | `{role,status,model_ids:string[]}` |
| GET /platform/channels | 数组 `{id,name,base_url,provider,data_region,status,revision?,allowed_cidrs?}`，永不返回 secret |
| POST /platform/channels | `{name,base_url,secret,provider:"openai",data_region,allowed_cidrs:string[]}` |
| PATCH /platform/channels/{id} | `{name?,status?,base_url?,secret?,data_region?,allowed_cidrs?}`；空secret不提交 |
| POST /platform/channels/{id}/probe | `{}` -> `{ok,message?,latency_ms?}`，明确提示可能产生供应商成本 |
| GET /platform/models | 数组 `{id,public_name,context_limit,output_limit,default_output_limit,enabled,revision,bindings:[...]}` |
| POST /platform/models | `{public_name,context_limit,output_limit,default_output_limit,supports_tools:boolean}` |
| PATCH /platform/models/{id} | `{enabled}` |
| PUT /platform/models/{id}/bindings | `{expected_revision,bindings:[{channel_id,upstream_model,priority,weight,max_inflight}]}` |
| POST /platform/price-versions | `{model_id,input_rate,output_rate,effective_at}`；单价微元/百万Token |
| GET /platform/cost-versions?cursor= | admin：数组 `{id,channel_id,channel_name,upstream_model,input_rate:string|null,output_rate:string|null,basis,effective_at,created_at}` |
| POST /platform/cost-versions | admin+近期再认证：`{channel_id,upstream_model,input_rate:string|null,output_rate:string|null,basis:"contract"|"invoice"|"unknown",effective_at}`；成本单价微元/百万Token，unknown保存null，合同/账单必须显式给单价 |
| GET /platform/reports/summary?tenant_id= | 平台角色：累计资金与请求字段同客户报表，加 `{known_provider_cost,unknown_provider_costs,unknown_probe_costs,projection_pending,as_of}`；省略tenant_id表示所有客户，探测待核对数始终为全平台数量 |
| GET /platform/costs?tenant_id=&cursor= | 平台角色：数组 `{id,tenant_id,tenant_name,request_id,channel_id,channel_name,upstream_model,cost_amount:string|null,cost_status,created_at}`，id为供应商尝试ID |
| POST /platform/cost-corrections | finance_operator+近期再认证：`{tenant_id,attempt_id,amount:string,basis:"contract"|"invoice",evidence_ref}`，带稳定Idempotency-Key；金额为此尝试完整成本，不是差额；服务端记录真实操作者 |
| GET /platform/credits | 数组 `{id,tenant_id,tenant_name?,business_ref,amount,state,requester,approver?,created_at}` |
| POST /platform/credits | `{tenant_id,business_ref,amount,proof_ref}` |
| POST /platform/credits/{id}/approve | `{}`；制单/审批不能同账号 |
| GET /platform/refunds | 数组 `{id,tenant_id,settlement_id,amount,state,requester,reason,created_at}` |
| POST /platform/refunds | `{tenant_id,settlement_id,amount,reason}`；仅退平台钱包 |
| POST /platform/refunds/{id}/approve | `{}`；原消费累计退款封顶 |
| GET /platform/reconciliations | 数组 `{id,request_id,tenant_id,reason,status,deadline,proposed_usage?}` |
| POST /platform/reconciliations/{id}/resolve | `{action:"release"|"settle",reason,input_tokens?,output_tokens?,evidence_ref?}` |
| POST /platform/reconciliations/{id}/approve | `{}`；不同财务账号，仍受24h截止和预占上限 |
| GET /platform/audit?cursor= | 数组 `{id,actor,action,target,tenant_scope?,created_at,sanitized_diff?}` |

报表是全生命周期累计，金额可超过 JavaScript 安全整数，保持十进制字符串。`projection_pending > 0` 明示报表延迟，钱包仍来自同步账本。平台“已知供应商成本”仅为已知部分，未知尝试另计；不计算或展示确认毛利。成本金额为空、状态为unknown或未识别时显示“待核对”，不把缺失成本显示成0。成本版本是追加历史，不修改已有尝试的成本快照；成本更正不改变客户收费或钱包余额。

## 恢复核对

| 接口 | 请求/字段 |
|---|---|
| GET /recovery/status | 平台角色：`{gate:{required,epoch,proposal_id?,requester?,approver?,evidence_ref?,reason?,proposed_at?,approved_at?,latched_at?},recovery_mode,restart_required}` |
| POST /recovery/propose | finance_operator+近期再认证：`{epoch,evidence_ref,reason}` -> gate；证据引用最长256、理由最长200，去空白后不能为空，提案成功后冻结 |
| POST /recovery/approve | 不同finance_approver+近期再认证：`{epoch,proposal_id}` -> `{gate,restart_required:true}`；服务端复查批次、提案、当前角色及账本 |

恢复入口仅处理已有门禁的核对，不提供开启或绕过门禁的按钮。批准只解除持久门禁，不关闭当前进程的恢复模式；界面明确提示交由运维移除 `RECOVERY_MODE` 配置并重启。若手动恢复模式没有 `gate.approved_at`，仅提示“尚无本批次审批记录”，不得把 `required=false` 或 `restart_required=true` 解释成已通过双人核对。冲突 `409 recovery_conflict` 时显示安全错误并要求刷新核对，不自动重放审批。

## 前端验收

- 生产构建与 TypeScript 检查通过；路由导航、表格、小屏布局可用。
- 未登录/MFA/再认证状态独立；CSRF随租户切换刷新。
- 请求失败显示本地安全消息与request_id，数据为空不造样例；分页保留游标。
- Key明文关闭即清除，不进入localStorage或URL；密钥字段使用密码输入。
- 人工核款、平台钱包退款、未知账单和待生效售价有清晰说明；不冒充线上支付。
- 客户只见本租户资金报表；平台成本明确区分已知/未知，成本版本的unknown不会被空输入转为0。
- 密码成功更改后必须重新登录；表单不进入localStorage、日志或URL。
- MFA 绑定可本地扫码或手动添加；重新生成不复用旧待绑定资料，离开页面或启用后清除临时二维码和秘密。
