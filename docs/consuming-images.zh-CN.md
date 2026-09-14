# 使用已发布的 PlatFarm 镜像

[English](consuming-images.md) | [简体中文](consuming-images.zh-CN.md)

**发版拆分**

| 仓库 | 打 `v*` | 产物 |
|---|---|---|
| [platfarmai/platfarm](https://github.com/platfarmai/platfarm) | Release images | GHCR：`auth` / `console` |
| [platfarmai/pctl](https://github.com/platfarmai/pctl) | Release pctl | 各平台 `pctl_*` |

**不发布**语言 `runtime-*` 基础镜像。模板里的 Dockerfile 已自包含。

CLI 从 [pctl Releases](https://github.com/platfarmai/pctl/releases) 下载。

## A. 不用 Go 也能起平台

见 [docker-compose-quickstart.zh-CN.md](docker-compose-quickstart.zh-CN.md) 路径 B（`pctl init` + `compose.release.yml`）。

## B / C

业务服务用 `templates/*-service` 的 Dockerfile；单独嵌 auth 见英文版 §C。
