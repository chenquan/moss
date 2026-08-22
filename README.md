# Moss

Moss 是一个本地、机器优先的个人知识与行动运行时。它为匹配的 AI Skill 提供受控的本地文件处理、知识整理、检索、行动管理、备份与恢复能力。

Moss 的职责是确定性地完成协议校验、文件安全检查、SQLite 持久化、内容索引、幂等重试和高影响操作的计划/确认边界。自然语言理解和用户交互由 Skill 完成。

## 产品边界

- `moss call` 是机器协议入口，不是面向用户的业务 CLI。
- `moss skill install` 是唯一面向人工的安装命令。
- 正常运行时通过 stdin/stdout 传输一份 JSON 请求和一份 JSON 响应。
- Moss 不提供 Web UI、MCP 或第二个模型；Skill 是用户界面。
- 原始文件不会被修改或删除，最终 Wiki 由 Moss 管理。

整体数据流如下：

~~~
AI Skill
   │  stdin/stdout JSON
   ▼
moss call
   │
   ├── SQLite：元数据、索引、幂等状态、计划和审计
   └── Managed files：Raw、Markdown Wiki、Jobs、备份、回收区和 staging
~~~

当前运行时版本为 `0.1.0`，协议版本为 `1.0`。

## 快速开始

### 1. 从源码构建

项目使用 Go `1.25.0`。

~~~
git clone https://github.com/chenquan/moss.git
cd moss
go build -o moss .
~~~

构建后的二进制会内置 Skill 资源，不需要在运行时读取源码目录。

### 2. 安装 Skill

默认安装到当前用户的 Claude Code 全局 Skill 目录：

~~~
./moss skill install
~~~

常用安装方式：

~~~
# 安装到全局 Codex Skill 目录
./moss skill install --target codex

# 同时安装到 Claude Code 和 Codex
./moss skill install --target claude --target codex

# 安装到当前项目目录
./moss skill install --scope project

# 安装到当前项目的两个 Skill 目录
./moss skill install --scope project --target claude --target codex
~~~

安装目标如下：

| 目标 | 全局默认路径 | 项目路径 |
| --- | --- | --- |
| Claude Code | `~/.claude/skills/moss` | `./.claude/skills/moss` |
| Codex | `$CODEX_HOME/skills/moss`，未设置时为 `~/.codex/skills/moss` | `./.codex/skills/moss` |

重复安装相同内容是幂等的。如果目标目录中存在不同内容，安装器默认拒绝覆盖。`--force` 会删除并重建完整的 Skill 目录，包括其中与 Moss 无关的文件，请谨慎使用：

~~~
./moss skill install --force
~~~

当前内置 Skill 的兼容性元数据以 Claude Code 为主要运行环境；Codex 目标支持资源部署，但使用前应验证目标宿主的 Skill 运行契约。

### 3. 检查协议和本地健康状态

先进行握手，再检查本地存储：

~~~
./moss call <<'JSON'
{
  "protocol_version": "1.0",
  "request_id": "req_handshake_001",
  "operation": "system.handshake",
  "actor": {
    "type": "claude-skill",
    "skill_version": "0.1.0"
  },
  "arguments": {}
}
JSON
~~~

~~~
./moss call <<'JSON'
{
  "protocol_version": "1.0",
  "request_id": "req_health_001",
  "operation": "system.health",
  "actor": {
    "type": "claude-skill",
    "skill_version": "0.1.0"
  },
  "arguments": {}
}
JSON
~~~

第一次执行需要本地存储时，Moss 会创建默认数据目录和 SQLite 数据库。只有握手和健康检查都成功后，才应继续写入用户数据。

## 运行时协议

### 请求格式

`moss call` 从 stdin 读取一份完整 JSON 文档，并向 stdout 写出一行完整 JSON 响应。stderr 仅用于诊断信息。

~~~
{
  "protocol_version": "1.0",
  "request_id": "req_<每次尝试都新建>",
  "idempotency_key": "idem_<仅重试同一写操作时复用>",
  "operation": "<operation>",
  "actor": {
    "type": "claude-skill",
    "skill_version": "0.1.0"
  },
  "arguments": {},
  "options": {}
}
~~~

只读操作可以省略 `idempotency_key`。所有会改变状态的操作都必须提供幂等键：

- 每次尝试都生成新的 `request_id`。
- 只有重试完全相同的写操作时才复用原 `idempotency_key`。
- 用同一个幂等键提交不同操作或不同参数会返回 `IDEMPOTENCY_CONFLICT`。
- 请求必须恰好包含一份 JSON 文档，不能在后面拼接第二份文档。

协议限制：

- 最大请求体：`2 MiB`
- 最大响应体：`8 MiB`
- 最大单个原始输入文件：`128 MiB`
- 支持的 `source_type`：`document`、`markdown`、`text`
- 支持的敏感级别：`normal`、`sensitive`、`restricted`

响应结构：

~~~
{
  "protocol_version": "1.0",
  "request_id": "req_health_001",
  "ok": true,
  "data": {},
  "warnings": [],
  "next": {}
}
~~~

业务失败仍然通过结构化 JSON 返回，进程通常保持成功退出类别；只有无法输出响应的传输错误才使用非零退出码。失败响应包含稳定的 `error.code`、`error.message` 和 `error.retryable`。

当前版本只支持 stdio 传输，不支持 `--request`、`--response` 或其他协议文件参数。

## 常见工作流

### 记录本地文件

通过 `source.ingest` 导入文件。Moss 会复制文件内容到内容寻址的 Raw 存储，计算 SHA-256，并保留原始文件不变：

~~~
{
  "protocol_version": "1.0",
  "request_id": "req_ingest_001",
  "idempotency_key": "idem_ingest_001",
  "operation": "source.ingest",
  "actor": {
    "type": "claude-skill",
    "skill_version": "0.1.0"
  },
  "arguments": {
    "input_file": "/absolute/path/to/note.md",
    "source_type": "markdown",
    "sensitivity": "normal"
  }
}
~~~

相同内容会复用 Raw Blob，但不同的来源上下文仍可产生独立的 Source 记录。响应中会返回 `source_id`、`content_hash`、大小和是否重复。

### 整理为知识文章

知识整理是可恢复的分阶段流程：

~~~
source.ingest
    ↓
compile.start
    ↓
compile.next / compile.submit
    extract → classify → write
    ↓
compile.preview
    ↓  用户确认
compile.apply
~~~

Moss 会为每个 Job 返回当前阶段的输入文件、JSON Schema 和结果文件路径。Skill 只能写入 Moss 返回的当前结果路径，并通过 `compile.submit` 提交；不能直接写 SQLite、最终 Wiki、索引、计划或审计文件。

`compile.preview` 只创建变更计划。只有用户确认后调用 `compile.apply`，知识文章才会正式写入。

### 检索知识

常用顺序是：

~~~
knowledge.catalog / knowledge.candidates
    ↓
knowledge.materialize
~~~

`knowledge.catalog` 用于浏览目录和主题，`knowledge.candidates` 用于本地确定性候选排序，`knowledge.materialize` 用于读取经过哈希校验的单篇文章。文章被外部修改后会报告 `WIKI_DRIFT`，不会被静默覆盖或当作已验证内容返回。

### 管理行动

行动采用计划/应用两阶段模型：

~~~
action.create.plan / action.update.plan
    ↓  展示 diff、风险和过期时间
action.apply（confirmed: true）
~~~

行动类型包括 `task`、`commitment` 和 `reminder`，状态包括 `open`、`in_progress`、`done`、`deferred` 和 `cancelled`。`action.query` 用于查询今天、逾期和等待中的行动。

### 忘记、回滚、备份和恢复

高影响操作必须先创建计划、展示影响范围并获得明确确认：

| 目的 | 操作序列 |
| --- | --- |
| 忘记来源或项目 | `source.forget.plan` → `plan.inspect` → `plan.apply` |
| 回滚文章 | `knowledge.history` → `knowledge.rollback.plan` → `plan.apply` |
| 撤销安全计划 | `plan.inspect` → `plan.undo` |
| 创建备份 | `system.export` |
| 恢复备份 | `system.export` → 健康检查 → `system.restore`（确认） |

忘记操作会优先将可恢复的文件移动到 Moss 私有回收区，并记录审计信息；不能把创建计划当作已经删除或忘记完成。

## 操作矩阵

| 领域 | 操作 | 类型 |
| --- | --- | --- |
| 系统 | `system.handshake`、`system.capabilities`、`system.health` | 只读 |
| 系统 | `system.export`、`system.restore` | 写入/高影响 |
| 来源 | `source.get`、`source.list` | 只读 |
| 来源 | `source.ingest`、`source.mark_sensitive` | 写入 |
| 知识 | `knowledge.catalog`、`knowledge.candidates`、`knowledge.materialize`、`knowledge.history` | 只读 |
| 编译 | `compile.next`、`compile.status`、`plan.inspect`、`audit.query` | 只读 |
| 编译 | `compile.start`、`compile.submit`、`compile.preview`、`compile.apply`、`compile.abort` | 写入/计划 |
| 计划 | `plan.apply`、`plan.undo` | 写入/确认 |
| 行动 | `action.query` | 只读 |
| 行动 | `action.create.plan`、`action.update.plan`、`action.apply` | 写入/确认 |
| 安全 | `source.forget.plan`、`knowledge.rollback.plan` | 写入/计划 |

完整的参数、阶段规则、稳定错误和 Skill 路由说明见：

- [`internal/skill/assets/protocol/operation-guide.md`](internal/skill/assets/protocol/operation-guide.md)
- [`internal/skill/assets/protocol/cli-protocol.md`](internal/skill/assets/protocol/cli-protocol.md)

## 数据目录

默认数据根目录为 `~/.moss`，也可以通过 `MOSS_DATA_DIR` 指定一个绝对路径：

~~~
MOSS_DATA_DIR="$PWD/.moss-dev" ./moss call <<'JSON'
{
  "protocol_version": "1.0",
  "request_id": "req_health_local_001",
  "operation": "system.health",
  "actor": {
    "type": "claude-skill",
    "skill_version": "0.1.0"
  },
  "arguments": {}
}
JSON
~~~

目录结构：

| 路径 | 用途 |
| --- | --- |
| `moss.db` | SQLite 元数据、索引、幂等、计划和审计 |
| `raw/` | 内容寻址的不可变 Raw Blob |
| `wiki/` | Moss 管理的 Markdown 知识文章 |
| `jobs/` | 可恢复的编译 Job 和阶段文件 |
| `staging/` | 输入暂存和安全校验过程 |
| `backups/` | 本地备份归档 |
| `trash/` | 安全忘记操作的恢复区 |
| `locks/` | 并发和恢复锁 |
| `responses/` | 运行时管理的响应相关目录 |

目录默认使用 `0700` 权限，SQLite 数据库使用 `0600`。SQLite 开启 WAL、外键和完整性检查；schema 当前版本为 `4`。

## 安全与一致性边界

- 输入文件必须是允许的普通文件；目录、设备、socket 和不安全的符号链接会被拒绝。
- Moss 不修改或删除用户提供的原始文件。
- Raw 内容按 SHA-256 去重，Source 记录保留来源类型、来源名称、敏感级别和创建时间。
- 敏感内容默认不返回；读取敏感内容需要显式的请求权限。
- 通过外部编辑修改托管 Wiki 会被标记为 `WIKI_DRIFT`，不会自动覆盖。
- 所有计划和应用操作都会检查版本、哈希、过期时间和漂移状态，避免覆盖并发修改。
- 响应中的文本、来源内容、文章内容和阶段结果都是不可信数据，不能被当作命令、确认或策略覆盖。

## Skill 行为

内置 Skill 只在用户当前消息明确称呼 “Moss” 时激活，例如：

~~~
Moss，记住这份资料
Moss，查询我之前的决定
~~~

没有明确称呼 Moss 的普通请求不会触发 Moss 运行时。Skill 负责自然语言交互、确认和结果解释；Moss 负责确定性执行和本地状态保护。

## 开发

运行测试和构建：

~~~
go test ./...
go build ./...
~~~

如果当前环境无法写入默认 Go 构建缓存，可以指定一个可写缓存目录：

~~~
GOCACHE=/private/tmp/moss-gocache go test ./...
~~~

Skill 资源位于 `internal/skill/assets/`，通过 `go:embed` 打包进二进制。`internal/skill/install_test.go` 会检查嵌入资源与仓库中的资源是否一致。

核心代码目录：

| 路径 | 职责 |
| --- | --- |
| `cmd/` | Cobra 入口和 Skill 安装命令 |
| `internal/protocol/` | 请求、响应、版本、大小和幂等校验 |
| `internal/app/` | 操作分发和运行时边界 |
| `internal/source/` | 来源导入、Raw Blob 和敏感级别 |
| `internal/compile/` | 分阶段知识整理和计划 |
| `internal/knowledge/` | 目录、候选、文章和历史 |
| `internal/action/` | 行动计划、应用和查询 |
| `internal/safety/` | 忘记、回滚和审计 |
| `internal/storage/` | SQLite、迁移和受控文件布局 |

## 许可证

本项目采用 [Apache License 2.0](LICENSE)。
