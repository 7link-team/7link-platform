# IAM 应用就绪验收记录

## 目标与范围
用户要求复核发布检查点、相关性二审、方案复审、一次修复及测试验收，全部提交推送 origin/IAM。
应用功能和代码验收包含 Redis Cluster/Sentinel 配置、客户端、TLS/ACL、错误处理；生产部署、灾备、运维演练和签字排除。
D34/D35保持：现有租户归属；PAT单租户/显式期限；退租与全局关闭申请分离，processing不自动设保留或擦除。

## 当前阶段与结论
ACCEPTANCE：应用/代码范围 PASS，无剩余已知阻塞项。
主线程已完成自审与原请求验收；三组原代理完成且空闲。
交付分支 IAM，上一基线34778e7；本轮提交标识以包含本记录的Git提交为准。
最终交付动作：精确暂存174个可交付文件、Conventional Commit、push origin/IAM并核对远端SHA；用户已有授权，无需再次询问。

## 实施
- authn v14 reauth / v15 PAT，federation v6本人链接，identity v5关闭申请；18 cookie-only + 3 platform operation。
- 退租/全局关闭原子撤凭据、最后管理员守卫和迟到OAuth授权围栏；本人链接不JIT、不提升角色、不替换会话。
- PostgreSQL实证修复：OAuth GORM跨表statement条件泄漏；authn六处+core一处重复UpdatedAt赋值。
- Redis standalone/cluster/sentinel、独立ACL/file-backed密码、TLS/mTLS、明确failure_policy、同slot Lua及未知结果不重放。
- 三语言个人功能、九页加载错误态、三处嵌套Alert翻译、PAT成功对话框布局。

## 最终证据
- 后端fmt/lint/coverage PASS：总90.1%，全部受管模块≥90%。
- 原make ci EXIT2不能伪称EXIT0：仅新增合同的旧架构分类/测试命名和4组迁移静态期望失败；
  架构修后PASS3.425s，4组真实三引擎迁移复验PASS146.127s。
  其余goose108.634s、lifecycle387.688s、performance121.604s及迁移测试通过；build/openapi-check独立补跑PASS。
- 全量make test-race PASS：app133.366s、authn144.298s、core93.687s、federation53.204s、identity39.058s、oauth61.512s，无DATA RACE。
- 最终Redis目标EXIT0：plain4.10s/全mTLS9.30s，含MOVED/ASK、响应丢失不重放、切主和双客户端。
  原Sentinel测试未等双方切主通知导致FAIL，改为只读CLIENT INFO确认实际新主连接，保留单次消费断言；无生产代码变更。最终lint再次PASS。
- 真实MySQL8.4完整脚本EXIT0；PG关闭组合六场景PASS16.415s。新迁移旧数据保留/9999期限/owner/墓碑守卫通过。
- make generate-check + 含untracked生成物hash PASS；前端API类型再生成不变；schema verify219文件/72表/33跨模块FK，旧207哈希全部不变。
- OpenAPI271/IAM257，新增21无删除；既有schema无变化，3个既有federation operation仅补303响应。
- 前端lint/typecheck/test/build PASS（59 IAM+85 UI），fixture58+9；最后PAT定向eslint、三脚本syntax/prettier PASS。
- 真实浏览器业务792/792、个人生命周期6/6、个人安全12/12 PASS，三组cleanup PASS。
  三语言/双端/多权限/两租户；真实RS256 IdP18 fresh、24 code exchange、0 protocol error。
  SAML证据为真实签名Go/HTTP测试，非浏览器。最终截图和PAT几何已复核。
- 告警规则promtool SUCCESS。最终diffcheck PASS，交付文件无日志、密钥、截图和构建产物。

## 边界与保留环境
Redis异步复制可丢消费/计数，不承诺切主严格exactly-once；Sentinel与数据节点使用同一TLS配置。
不支持旧writer混跑，不把新表升降级测试称为在线无损回滚。
UAT保留：http://127.0.0.1:18091 → backend :18080，容器iam-uat-3e85f198cebd；旧preserve备份保留。
binary SHA256 70b55f3af4e092c0037708d2b93d7d9da80b376bebbce0b8fc31d4f46513e52b。
生命周期合成账号停用、租户软删除，保留processing元数据；不声称物理清空。
测试容器已自动清理，仅UAT运行；Go临时目录与日志均ignored，不纳入提交。
日志在apps/backend-api/.temp/personal-*.log，浏览器报告在.temp/iam-uat/browser/{route-matrix,personal-security,personal-lifecycle}/report.json。
完整可复跑证据和限制见apps/backend-api/docs/iam-release-checkpoints.md。
最后检查点：2026-09-10，最终Redis两模式和lint通过，进入提交推送。
