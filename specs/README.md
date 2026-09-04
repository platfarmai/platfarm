# specs/ — 变更规范流

轻量规范驱动工作流（设计见 docs/architecture-v2.md §6）：探索 → 提案 → 实现 → 审查 → 归档。

```
specs/
├── changes/NNN-短名/
│   ├── proposal.md    # 动机、影响面、service.yaml 差异
│   └── tasks.md       # 实施清单（AI 执行的锚点）
└── archive/           # 完成后整目录移入
```

## 提案模板（proposal.md）

```md
# NNN: <标题>
## 动机
## 方案（含 service.yaml / 契约变更 diff）
## 影响面（涉及哪些服务/平台文件）
## 验收标准（可执行的验证命令）
```

规则：涉及**平台契约**（claims 结构、service.yaml 字段、网关行为、保留路由段）的变更必须走提案；单服务内部业务变更不强制。
