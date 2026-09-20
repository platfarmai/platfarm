# 014 实施清单

[English](tasks.md) | [简体中文](tasks.zh-CN.md)

- [x] auth：users list 接受 limit/offset/q；返回 {items,total,limit,offset}；ILIKE 搜索；limit 上限 100
- [x] svc-users 后端：limit/offset/q 透传 auth
- [x] svc-users SPA：防抖搜索框 + 上一页/下一页 + 总数
- [x] 文档 ADR #29
- [x] QA：默认 20+total / limit+offset 窗口 / q 过滤 / 后台搜索+翻页
