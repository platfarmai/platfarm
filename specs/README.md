# specs/ — Change Spec Workflow

[English](README.md) | [简体中文](README.zh-CN.md)

Lightweight specification-driven workflow (see docs/architecture-v2.md §6): explore → propose → implement → review → archive.

```
specs/
├── changes/NNN-short-name/
│   ├── proposal.md        # English (primary): motivation, blast radius, service.yaml diffs
│   ├── proposal.zh-CN.md  # Chinese
│   ├── tasks.md           # English (primary): implementation checklist (AI execution anchors)
│   └── tasks.zh-CN.md     # Chinese
└── archive/               # Move the whole directory here when done
```

## Language convention

Same as the repository README: **English is primary, Chinese is secondary**.

- `proposal.md` / `tasks.md` — English, the canonical source AI agents and CI read
- `proposal.zh-CN.md` / `tasks.zh-CN.md` — Chinese, kept in sync for human readers
- Both files open with a language switcher link

## Proposal template (`proposal.md`)

```md
# NNN: <Title>
## Motivation
## Design (include service.yaml / contract diffs)
## Blast radius (services / platform files touched)
## Acceptance (executable verification commands)
```

Rule: changes that touch the **platform contract** (claims shape, service.yaml fields, gateway behaviour, reserved route prefixes) **must** go through a proposal. Purely internal business changes inside a single service do not.
