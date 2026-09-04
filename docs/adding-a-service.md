# 服务接入教程

平台可插拔的核心承诺：**新增服务 = 一个目录 + 一次 sync，不改任何平台文件与已有服务。**

## 五步接入

### 1. 生成骨架

```bash
./tools/platctl/platctl.exe new svc-<name> --lang py
```

产出 `services/svc-<name>/`：

| 文件 | 作用 |
|---|---|
| `service.yaml` | 服务清单：挂载点、公开路由、限流、环境变量声明（平台唯一读取的契约） |
| `main.py` | FastAPI 骨架，内置六步验签中间件（`identity()`）与 `/me` `/public/ping` 示例路由 |
| `Dockerfile` | alpine + python3，可整体替换为任意技术栈（保住 8080 端口与 /healthz 即可） |
| `tests/contract/contract_test.py` | 契约测试三件套，`platctl check --e2e` 在容器内执行 |
| `openapi.yaml` | 接口契约，platctl sync 聚合到网关 `/docs`（规划中） |

### 2. 写业务

- 身份：调用骨架里的 `identity(authorization)` 拿 `userId / tenantId / role`，资源归属自己判（403 自己发）
- 数据：需要存储时在 `service.yaml` 声明 `data.database: pf_<name>`，连自己的库；**禁止**连其它服务的库
- 服务间调用：内网直连 `http://svc-xxx:8080`，转发用户 JWT；参见 [architecture-v2.md](architecture-v2.md) 附录 E（gRPC）

### 3. 调整清单（按需）

```yaml
mount:
  path: /api/<name>        # 不可占用保留段 /auth /platform /internal /docs
  public_routes:
    - GET /public/*        # 免登录路由
limits:
  rate_per_minute: 60      # 网关限流
```

### 4. 同步与启动

```bash
./tools/platctl/platctl.exe sync          # 校验清单 → 生成 kong.yml + compose 片段
docker compose up -d --build
docker compose restart gateway        # 网关加载新路由
```

sync 会拒绝：路由冲突、占用保留段、id 与目录不一致、数据库命名不带 `pf_` 前缀。

### 5. 验证

```bash
./tools/platctl/platctl.exe check --e2e   # 契约三件套：无 token 401 / 有效 200 / 篡改 401
```

三件套全绿 = 接入完成。业务自身的测试放 `tests/`，由服务自己维护。

## 常见问题

- **网关返回 401 但我带了 token**：检查 token 是否 `tokenType: access`（refresh token 不能调业务接口）、是否过期（access 2h）、`.keys/` 公钥是否与签发方一致（删过 .keys 需重新 sync + 重建全部服务）
- **改了 service.yaml 没生效**：忘了 `sync` + `restart gateway`
- **需要 Go/Node 模板**：暂只有 py；复制 `templates/py-service` 结构自行改写，保持 service.yaml 与契约测试语义一致即可
