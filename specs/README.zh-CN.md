# specs/ — 变更规范流

[English](README.md) | [简体中文](README.zh-CN.md)

轻量规范驱动工作流（设计见 docs/architecture-v2.md §6）：探索 → 提案 → 实现 → 审查 → 归档。

```
specs/
├── changes/NNN-短名/
│   ├── proposal.md        # 英文（主版本）：动机、影响面、service.yaml 差异
│   ├── proposal.zh-CN.md  # 中文
│   ├── tasks.md           # 英文（主版本）：实施清单（AI 执行的锚点）
│   └── tasks.zh-CN.md     # 中文
└── archive/               # 完成后整目录移入
```

## 语言约定

与仓库 README 一致：**英文为主，中文为辅**。

- `proposal.md` / `tasks.md` — 英文，AI 与 CI 读取的权威源
- `proposal.zh-CN.md` / `tasks.zh-CN.md` — 中文，给人读，保持同步
- 两个文件头部互挂语言切换链接

## 提案模板（`proposal.md`）

```md
# NNN: <标题>
## 动机
## 方案（含 service.yaml / 契约变更 diff）
## 影响面（涉及哪些服务/平台文件）
## 验收标准（可执行的验证命令）
```

规则：涉及**平台契约**（claims 结构、service.yaml 字段、网关行为、保留路由段）的变更必须走提案；单服务内部业务变更不强制。
