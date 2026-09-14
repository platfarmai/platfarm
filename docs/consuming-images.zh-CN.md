# 使用已发布的 PlatFarm 镜像

[English](consuming-images.md) | [简体中文](consuming-images.zh-CN.md)

**发版拆分**

| 仓库 | 打 `v*` | 产物 |
|---|---|---|
| [platfarmai/platfarm](https://github.com/platfarmai/platfarm) | Release images | GHCR：`auth` / `console` / `runtime-*` |
| [platfarmai/pctl](https://github.com/platfarmai/pctl) | Release pctl | 各平台 `pctl_*` + `SHA256SUMS.txt` |

二进制从 [pctl Releases](https://github.com/platfarmai/pctl/releases) 下载，**不要**提交进 git。

---

## A. 不用 Go 也能起平台

```bash
mkdir platfarm-run && cd platfarm-run
VER=v0.0.1   # pctl 版本，与平台镜像标签独立
curl -fsSL -o pctl "https://github.com/platfarmai/pctl/releases/download/${VER}/pctl_${VER}_linux_amd64"
chmod +x pctl
curl -fsSL https://raw.githubusercontent.com/platfarmai/platfarm/main/deploy/compose.release.yml -o compose.yml
curl -fsSL https://raw.githubusercontent.com/platfarmai/platfarm/main/deploy/env.release.example -o .env
./pctl init .
docker compose --profile bundled-db up -d
```

`pctl init` 生成本地 `.keys/` 与最小 `gateway/kong.yml`。完整说明见 [docker-compose-quickstart.zh-CN.md](docker-compose-quickstart.zh-CN.md) 路径 B。

旧文档写「在有 pctl 的机器上 sync 再拷贝 `.keys/`」绕了一圈；Release 里的二进制本身就是 pctl，应直接在运行目录执行 `pctl init`。

auth 在空 volume 上也会自建密钥，但 Kong 仍需要匹配的公钥写进 `kong.yml`，所以正式流程仍用 `pctl init` / `pctl sync`。

## B / C

业务 `FROM runtime-*`、单独嵌 auth：见英文版 §B / §C。
