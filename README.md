# Luna PaaS Cloud

Luna PaaS Cloud 是一个适合个人或小团队自托管的轻量 PaaS。它连接 GitHub 仓库，自动注入 GitHub Actions 工作流，将应用构建为 Docker 镜像并推送到阿里云容器镜像服务（ACR），随后通过本机 Docker 或 SSH 节点完成发布。

> 项目仍处于早期阶段，建议先在测试环境评估，不要直接用于关键生产负载。

## 功能特性

- OAuth2 登录
- 自动创建和维护 GitHub Actions 构建工作流
- 支持 Vue、Python、Java 和 Go 项目
- 支持本机及 SSH 远程 Docker 节点
- 应用环境变量、持久卷、健康检查和重启策略配置
- 构建记录、发布记录、实时日志与容器运行指标
- 一键发布、历史版本回滚及钉钉机器人通知
- GitHub PAT、ACR、SSH 凭证和敏感环境变量采用 AES-256-GCM 加密存储

## 工作流程

```text
GitHub push
    │
    ▼
GitHub Actions ──构建并推送──▶ ACR
    │
    └──回调──▶ Luna PaaS Cloud ──Docker Compose/SSH──▶ 部署节点
```

## 技术栈

- 后端：Go 1.24、GORM、MySQL 8.4
- 前端：Vue 3、TypeScript、Vite、Tailwind CSS
- 部署：Docker、Docker Compose、SSH
- CI/CD：GitHub Actions、阿里云 ACR

## 快速开始

### 运行要求

- Docker Engine 与 Docker Compose v2
- 一个可用的 MySQL 8 实例（默认 Compose 配置会自动启动）
- 一个能返回用户手机号的 OAuth2 服务及客户端凭证
- GitHub fine-grained personal access token
- 阿里云容器镜像服务实例和命名空间
- 一个能被 GitHub 托管 Runner 访问的公网 HTTPS 地址

目标部署节点也需要安装 Docker Engine 和 Docker Compose v2。SSH 节点应允许指定用户执行 Docker 命令。

### 1. 配置环境变量

```bash
cp .env.example .env
openssl rand -base64 32
```

将第二条命令的结果写入 `.env` 的 `PAAS_MASTER_KEY`，并完成其余配置：

| 变量 | 必填 | 说明 |
| --- | --- | --- |
| `MYSQL_PASSWORD` | 是 | PaaS 数据库用户密码 |
| `MYSQL_ROOT_PASSWORD` | 是 | MySQL root 密码 |
| `PAAS_MASTER_KEY` | 是 | Base64 编码的 32 字节加密密钥 |
| `PAAS_PUBLIC_URL` | 是 | 后端公网 HTTPS 地址，供 Actions 回调 |
| `PAAS_FRONTEND_URL` | 是 | 前端访问地址 |
| `PAAS_SECURE_COOKIES` | 否 | 是否仅通过 HTTPS 发送会话 Cookie，默认 `true` |
| `PAAS_HTTP_PORT` | 否 | 对外 HTTP 端口，默认 `32900` |
| `OAUTH_CLIENT_ID` | 是 | OAuth2 客户端 ID |
| `OAUTH_CLIENT_SECRET` | 是 | OAuth2 客户端密钥 |
| `OAUTH_REDIRECT_URL` | 是 | OAuth2 回调地址，通常为 `<公网地址>/api/auth/callback` |
| `OAUTH_SCOPE` | 否 | OAuth2 scope，默认 `openid profile` |
| `OAUTH_PHONE_PATH` | 否 | 用户信息响应中的手机号字段路径，默认 `phone` |
| `ACR_REGISTRY` | 是 | ACR Registry 地址 |
| `ACR_NAMESPACE` | 是 | ACR 命名空间 |

如需使用其他 OAuth2 服务，可在 `.env` 中额外设置 `OAUTH_AUTH_URL`、`OAUTH_TOKEN_URL` 和 `OAUTH_USER_URL`。

### 2. 启动服务

```bash
docker compose up -d --build
docker compose ps
```

默认访问地址为 <http://localhost:32900>。查看日志：

```bash
docker compose logs -f backend frontend
```

### 3. 添加首个登录用户

服务首次启动时会自动创建数据库表。在 MySQL 中添加允许登录的手机号：

```bash
docker compose exec mysql mysql -upaas -p paas
```

```sql
INSERT INTO allowed_users (id, phone, enabled, created_at, updated_at)
VALUES (UUID(), '13800000000', 1, NOW(), NOW());
```

### 4. 完成平台设置

登录后进入“系统设置”，填写：

- GitHub fine-grained PAT
- ACR 用户名和密码
- 钉钉机器人 Webhook（可选）

PAT 应仅授权需要部署的仓库，并授予 Metadata 读取以及 Contents、Workflows、Actions、Secrets 的读写权限。平台会创建 `.github/workflows/paas-build.yml`；如果仓库中已有同名且并非平台管理的文件，平台不会覆盖。

### 5. 创建部署

1. 在 ACR 命名空间中预先创建与 GitHub 仓库同名（小写）的私有镜像仓库。
2. 添加本机或 SSH 部署节点。
3. 创建应用，填写 GitHub 仓库、运行时、构建路径和端口等配置。
4. 等待首次 GitHub Actions 构建回调并自动发布。

远程节点首次测试时，如果未填写主机指纹，错误信息会显示观测到的 `SHA256:...` 指纹。请通过可信渠道核验后再保存，避免中间人攻击。

## 本地开发

后端：

```bash
cd backend
go test ./...
go run ./cmd/server
```

后端直接运行时需自行提供 `PAAS_DATABASE_URL`、`PAAS_MASTER_KEY` 等环境变量。

前端：

```bash
cd frontend
npm ci
npm run dev
```

运行全部检查或构建：

```bash
make test
make build
```

统一格式化 Go、Vue、TypeScript 和 CSS 代码：

```bash
make format
make format-check
```

## 安全说明

- 请永久备份 `PAAS_MASTER_KEY`。密钥丢失后，数据库中已有的加密凭证无法恢复。
- `.env` 包含密钥且已被 `.gitignore` 忽略；提交前仍应使用 `git status` 检查。
- 生产环境必须使用 HTTPS，并保持 `PAAS_SECURE_COOKIES=true`。
- Actions 回调接口由每个应用独立的随机 Bearer Token 保护。
- 应用端口默认只绑定部署节点的 `127.0.0.1`；对外服务应通过受控的反向代理暴露。
- “允许访问宿主机服务”默认关闭；启用后应用可通过 `host.docker.internal` 访问宿主机。宿主机服务仍应启用认证并限制监听地址和防火墙规则。
- 挂载 Docker Socket 等同于授予容器较高的宿主机权限，请限制平台访问者和宿主机权限。

## 项目结构

```text
.
├── backend/       # Go API、GitHub 集成与部署执行器
├── frontend/      # Vue 管理界面
├── compose.yaml   # 本地/自托管编排
└── Makefile       # 测试与构建入口
```

## 参与贡献

欢迎提交 Issue 和 Pull Request。提交前请确保 `make test` 通过，并且不要提交 `.env`、访问令牌、私钥或其他敏感信息。

## 开源许可

本项目基于 [GNU General Public License v2.0](./LICENSE) 发布。分发或修改本项目时，请遵守许可证中的相应义务。
