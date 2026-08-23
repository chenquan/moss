## Why

Moss 已经保存了带来源和版本的事实、受管文章以及行动，但 Skill 目前只能分别检索这些数据，无法直接回答“哪些决定需要复核、它们基于什么证据、是否已经被后续事实取代”。现在先增加一个只读洞察层，可以验证决策审阅场景的价值，同时保持 Moss 的本地、确定性和确认边界。

## What Changes

- 新增 `knowledge.insights` 只读协议操作，返回决策事实、版本状态、新鲜度、引用和关联文章/行动。
- 派生 stale、superseded、retracted 和来源不可用等审阅信号，并保持稳定排序和结果上限。
- 通过现有文章引用、事实版本和行动来源建立可解释关联；明确不把共享来源推断为因果关系。
- 沿用现有敏感内容过滤、受管 Wiki 漂移保护和来源遗忘语义。
- 更新协议能力发现、Moss Skill 路由、操作指南、检索工作流和 README 能力矩阵。
- 不新增决策/关系表，不自动写入或修改事实、文章和行动；显式冲突图、行动结果和 context bundle 留待后续变更。

## Capabilities

### New Capabilities

- `knowledge-insights`: 提供基于现有事实、文章、来源和行动的只读决策审阅洞察。

### Modified Capabilities

无。现有知识检索操作的契约保持不变；新增洞察作为独立能力提供。

## Impact

影响 `internal/knowledge`、`internal/app`、`internal/protocol`、Moss Skill 资源、README 和测试。首期不需要数据库迁移或新增外部依赖；新增操作通过现有 stdio JSON 协议暴露。
