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

### 1. 使用 `go install` 安装

需要 Go `1.25.0` 或更高版本：

~~~
go install github.com/chenquan/moss@latest
moss skill install
~~~

`go install` 会将 `moss` 安装到 Go 的 bin 目录（由 `GOBIN` 或 `GOPATH` 决定）。如果终端找不到 `moss`，请将 `go env GOPATH` 返回的 bin 目录加入 `PATH`。

安装完成后，可以直接使用 `moss call` 执行协议调用：

~~~
moss call <<'JSON'
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

### 2. 从源码构建

项目使用 Go `1.25.0`。

~~~
git clone https://github.com/chenquan/moss.git
cd moss
go build -o moss .
~~~

构建后的二进制会内置 Skill 资源，不需要在运行时读取源码目录。

### GitHub Actions 自动打包

仓库内置 GitHub Actions：每次 push 或 pull request 会在 Linux 和 macOS 上执行测试、vet、race 检查，并用 GoReleaser 生成跨平台 snapshot。推送 `v` 开头的 tag（例如 `v0.1.0`）后，Release workflow 会自动发布以下平台的压缩包和 `checksums.txt`：

- macOS、Linux
- `amd64` 和 `arm64`

当前存储锁和目录权限实现依赖 Unix 语义，因此发布矩阵暂不包含 Windows；Windows 支持需要单独完成平台锁和权限适配。

发布使用 `.goreleaser.yaml`，本地可以用相同命令预览打包结果：

~~~bash
goreleaser release --snapshot --clean
~~~

### 3. 安装 Skill

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

### 4. 检查协议和本地健康状态

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

如果健康检查返回 `recovery-required`，不要删除或编辑 `staging/` 下的恢复标记。使用 `system.health` 返回的标记文件名调用受控的 `system.recover`：

~~~
./moss call <<'JSON'
{
  "protocol_version": "1.0",
  "request_id": "req_recover_001",
  "idempotency_key": "idem_recover_001",
  "operation": "system.recover",
  "actor": {
    "type": "claude-skill",
    "skill_version": "0.1.0"
  },
  "arguments": {
    "recovery_id": "<system.health 返回的标记文件名>"
  }
}
JSON
~~~

恢复会根据 SQLite 和受管文件的前后哈希清理已提交状态或回滚未提交状态；遇到不匹配的文件会保留现场并返回冲突，不会覆盖未知内容。

## 使用案例

安装 Skill 后，在支持的 AI 宿主中直接用自然语言称呼 “Moss”。用户不需要手写 JSON 或调用内部协议；Skill 会负责理解请求、展示结果和请求确认，Moss 负责确定性地执行下面的操作。

| 目标 | 示例请求 | 处理流程 |
| --- | --- | --- |
| 记住资料 | `Moss，记住我刚上传的会议纪要，并整理成知识文章。` | 导入来源 → 分阶段整理 → 展示预览 → 确认后写入文章 |
| 查找知识 | `Moss，查找我之前关于项目 X 的决定，只返回已验证内容。` | 浏览目录 → 确定性候选排序 → 读取哈希校验后的文章 |
| 记录行动 | `Moss，提醒我周五跟进项目 X 的合同。` | 创建行动计划 → 展示变更 → 确认后应用 |
| 保护敏感资料 | `Moss，这份资料包含隐私，请按敏感内容保存。` | 标记敏感级别 → 默认隐藏 → 读取时要求显式权限 |
| 忘记或回滚 | `Moss，忘记与项目 X 相关的来源。` | 创建影响范围计划 → 查看审计信息 → 确认后执行；回滚文章也遵循相同确认边界 |
| 备份和恢复 | `Moss，检查本地状态并导出一份备份。` | 健康检查 → 创建备份；若存在中断变更，先按恢复标记执行受控恢复 |

高影响请求不会直接修改数据。例如忘记来源、回滚文章、应用行动和恢复备份，Moss 都会先返回计划、影响范围和风险，只有 Skill 获得明确确认后才会继续。

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

编译默认拒绝敏感或受限来源及上下文。确实需要处理时，必须在请求的 `options` 中显式设置 `"allow_sensitive": true`；这项权限不替代 `compile.apply` 的独立确认。

### 检索知识

普通知识问题的常用顺序是：

~~~
knowledge.catalog / knowledge.candidates
    ↓
knowledge.materialize
~~~

`knowledge.catalog` 用于浏览目录和主题，`knowledge.candidates` 用于本地确定性候选排序，`knowledge.materialize` 用于读取经过哈希校验的单篇文章。文章被外部修改后会报告 `WIKI_DRIFT`，不会被静默覆盖或当作已验证内容返回。

决策审阅问题可以先调用 `knowledge.insights`。它从现有决策事实、事实版本、来源引用、文章和行动中派生待复核信号，包括 `stale`、`superseded`、`retracted` 和 `evidence_unavailable`。查询支持可选的 `topic` 和 `limit`；结果按审阅优先级稳定排序，并限制决策、引用、文章和行动的返回数量。

使用要点：

- `superseded` 既可能表示同一事实的旧版本，也可能通过 `superseded_by_fact_id` / `superseded_by_version` 指向另一个明确替代它的决策事实。
- `evidence_unavailable` 表示引用来源或抽取工件当前不可验证；不要把该决策当作已有完整证据支持的结论。
- 行动关联会标记为 `shared_source`，只表示共享证据，不表示因果关系；文章只返回经过受管路径和哈希检查的元数据，不内联未经验证的正文。
- 敏感或受限的决策及关联默认被过滤；只有在确有必要时，才在请求 `options` 中显式设置 `"allow_sensitive": true`。
- `knowledge.review.scan` 是显式、只读的维护扫描，支持 `topic`、`limit`、`as_of` 和 `missing_result_after_hours`。它会返回 stale/superseded/retracted、证据不可用、文章漂移、到期复核、显式 `contradicts`、长期无结果行动和结果反馈缺口；不会创建行动、关系、通知或修改知识。
- `knowledge.context.bundle` 返回有上限的事实、决策、显式关系、来源定位、受管文章路径/版本/哈希、行动、行动结果和审阅信号。文章正文默认不内联，选定后继续调用 `knowledge.materialize` 做哈希校验和正文读取。
- 六类关系只能显式写入：`supports`、`contradicts`、`supersedes`、`depends_on`、`produces`、`resulted_in`。共享来源、文本相似或共同主题不会自动推断冲突或因果；关系在编译批次中与文章/事实原子应用。

典型请求只需要提供操作和可选参数：

~~~json
{
  "operation": "knowledge.insights",
  "arguments": {
    "topic": "发布策略",
    "limit": 10
  }
}
~~~

敏感或受限文章、来源和历史默认不出现在检索结果中；读取需要显式传入：

~~~
"options": {
  "allow_sensitive": true
}
~~~

`knowledge.history` 会逐个检查请求历史版本的敏感级别，即使当前版本是普通内容，只要历史结果包含未授权的敏感版本，也会返回 `SENSITIVITY_DENIED`，不会返回部分历史内容。

### 管理行动

行动采用计划/应用两阶段模型：

~~~
action.create.plan / action.update.plan
    ↓  展示 diff、风险和过期时间
action.apply（confirmed: true）
~~~

行动类型包括 `task`、`commitment` 和 `reminder`，状态包括 `open`、`in_progress`、`done`、`deferred` 和 `cancelled`。`action.query` 用于查询今天、逾期和等待中的行动。

行动结果使用独立的确认流：

~~~
action.result.plan
    ↓  展示结果状态、摘要、来源、风险和过期时间
action.result.apply（confirmed: true）
    ↓
action → produces → action_result
~~~

结果状态包括 `succeeded`、`failed`、`partial`、`cancelled` 和 `unknown`。应用结果不会自动把行动标记为 `done`，也不会自动更新事实或文章；后续知识反馈必须通过显式编译关系（例如 `action_result → resulted_in → fact`）完成。

### 忘记、回滚、备份和恢复

高影响操作必须先创建计划、展示影响范围并获得明确确认：

| 目的 | 操作序列 |
| --- | --- |
| 忘记来源或项目 | `source.forget.plan` → `plan.inspect` → `plan.apply` |
| 回滚文章 | `knowledge.history` → `knowledge.rollback.plan` → `plan.apply` |
| 撤销安全计划 | `plan.inspect` → `plan.undo` |
| 创建备份 | `system.export` |
| 恢复备份 | `system.export` → 健康检查 → `system.restore`（确认） |
| 处理文件/SQLite 变更中断 | `system.health` → `system.recover` |

忘记操作会优先将可恢复的文件移动到 Moss 私有回收区，并记录审计信息；不能把创建计划当作已经删除或忘记完成。
文件与 SQLite 变更中断时，健康检查会阻止后续写操作，直到对应恢复标记被受控处理。

## 操作矩阵

| 领域 | 操作 | 类型 |
| --- | --- | --- |
| 系统 | `system.handshake`、`system.capabilities`、`system.health` | 只读 |
| 系统 | `system.export`、`system.restore` | 写入/高影响 |
| 系统 | `system.recover` | 写入/恢复 |
| 来源 | `source.get`、`source.list` | 只读 |
| 来源 | `source.ingest`、`source.mark_sensitive` | 写入 |
| 知识 | `knowledge.catalog`、`knowledge.candidates`、`knowledge.insights`、`knowledge.review.scan`、`knowledge.context.bundle`、`knowledge.materialize`、`knowledge.history` | 只读 |
| 编译 | `compile.next`、`compile.status`、`plan.inspect`、`audit.query` | 只读 |
| 编译 | `compile.start`、`compile.submit`、`compile.preview`、`compile.apply`、`compile.abort` | 写入/计划 |
| 计划 | `plan.apply`、`plan.undo` | 写入/确认 |
| 行动 | `action.query` | 只读 |
| 行动 | `action.create.plan`、`action.update.plan`、`action.apply`、`action.result.plan`、`action.result.apply` | 写入/确认 |
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
| `extractions/` | 带 provenance 和哈希校验的不可变 extraction 文件 |
| `staging/` | 输入暂存和安全校验过程 |
| `backups/` | 本地备份归档 |
| `trash/` | 安全忘记操作的恢复区 |
| `locks/` | 并发和恢复锁 |
| `responses/` | 运行时管理的响应相关目录 |

目录默认使用 `0700` 权限，SQLite 数据库使用 `0600`。SQLite 开启 WAL、外键和完整性检查；schema 当前版本为 `5`。涉及受管文件和 SQLite 的变更会持有数据根锁，并在 SQLite 提交前保留恢复标记。

## 安全与一致性边界

- 输入文件必须是允许的普通文件；目录、设备、socket 和不安全的符号链接会被拒绝。
- Moss 不修改或删除用户提供的原始文件。
- Raw 内容按 SHA-256 去重，Source 记录保留来源类型、来源名称、敏感级别和创建时间。
- 敏感内容默认不返回；读取敏感内容需要显式的请求权限。
- 编译上下文默认拒绝未授权的敏感来源、文章和事实；历史读取按每个版本重新检查权限。
- 通过外部编辑修改托管 Wiki 会被标记为 `WIKI_DRIFT`，不会自动覆盖。
- 应用、回滚、忘记撤销和敏感级别传播都会同步文章、引用和 FTS 索引；损坏或哈希不匹配的 extraction 不会继续作为活动结果。
- 所有计划和应用操作都会检查版本、哈希、过期时间和漂移状态，避免覆盖并发修改。
- 文件与 SQLite 的协调变更使用数据根锁和恢复标记；未解决的恢复状态会阻止后续写操作。
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
go vet ./...
go build ./...
openspec validate --all --strict
~~~

如果当前环境无法写入默认 Go 构建缓存，可以指定一个可写缓存目录：

~~~
GOCACHE=/private/tmp/moss-gocache go test ./...
~~~

发布前应使用隔离的 `MOSS_DATA_DIR` 运行真实二进制，覆盖握手、健康检查、来源导入、编译全阶段、计划应用/撤销、知识历史、敏感权限、行动、忘记应用/撤销、导出/恢复和最终健康检查；当前实现已完成这条全流程验证。

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
