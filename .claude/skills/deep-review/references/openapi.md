# Swagger/OpenAPI 评审准则

`deep-review` 在 HTTP/API 相关改动中使用本准则；`deep-audit` 对含 HTTP API 的审计范围必须使用。

## 证据输入

1. 找到仓库实际的规范生成命令或运行时文档端点，生成最终 JSON/YAML。
2. 提取全部相关 operation：method、path、operationId、tags、security、parameters、requestBody、responses。
3. 与运行时路由注册集合对账；对声明与实现不一致处发真实请求或运行路由测试。
4. 改动评审要与基线规范做结构化 diff；全仓审计要统计完整 operation 集合，不能只抽看几个 handler。

无法生成规范时要明确报告阻塞原因和未验证范围，不能把源码注解当成最终 Swagger 的替代证据。

## 评审维度

### 路径与资源

- 遵循仓库既有的一种风格：大小写、单复数、分词、版本前缀和尾斜杠不得混用。
- 路径表达稳定资源及真实从属关系；避免把筛选条件塞进 path，也避免无所有权关系的深层嵌套。
- CRUD 优先用 HTTP 方法表达。确实是领域动作时可使用动作端点，但同类动作必须采用统一位置和命名。
- path 模板变量与 parameter 名完全一致，同一业务标识在各接口使用同一名称。

### 参数与 HTTP 语义

- path 参数必须 `required: true`，并声明准确类型、format、范围或 pattern；可选条件放 query。
- query/header/cookie/body 的位置符合语义；同一值不得在多个位置形成优先级不明的重复输入。
- 分页、过滤、搜索、排序、字段选择和数组序列化在全 API 中复用统一名称、默认值、边界与编码方式。
- GET/HEAD 不依赖请求体；PUT 的整体替换和幂等性、PATCH 的部分更新、POST 的创建/动作、DELETE 的结果语义清楚。
- 成功与失败状态码、空响应、异步操作、创建后的资源定位与实际 handler 一致。

### Schema、命名与安全

- operationId 全局唯一、稳定且遵循一种 action/resource 顺序；tags 与模块边界一致。
- 字段命名、ID/time/enum format、required/nullable/default/example 与序列化及校验行为一致。
- 同一分页 envelope、错误模型、鉴权 header 和公共对象使用共享 schema，避免形同义不同的复制。
- 每个 operation 的 security 与运行时鉴权一致；公开接口显式可辨，敏感字段不进入请求示例或响应 schema。
- 请求与响应若权限或字段集合不同，应使用不同 schema，不能因复用而暴露只写或只读字段。

### 兼容性与完整性

- 对比基线识别删除/改名 path、method、参数、状态码或 schema，新增 required、收窄枚举/范围等破坏性变化。
- 检查注册路由无文档、规范 operation 无实现、同一路径方法冲突和重复 operationId。
- 风格不一致只有在违反明确本仓约定，或会造成客户端生成、网关、缓存、鉴权、可发现性或维护成本时才作为发现。

## 应留下的不变量

修复后优先保留集合测试：运行时路由集合等于规范 operation 集合；operationId 全局唯一；path 参数声明完整且同名；
同类分页/错误/security 契约一致。测试必须读取实际生成规范，新注册路由应自动进入断言范围。
