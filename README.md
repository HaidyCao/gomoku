# 五子棋 Agent 对战

本项目是一个本地五子棋游戏：支持人机对战，也支持两个 Agent 机机对战。浏览器负责人类落子或观战，Agent 通过复制的提示词调用 HTTP API 参与对局。

## 运行

```bash
cd frontend
npm install
npm run build
cd ..
go run .
```

打开 `http://localhost:8080`。

如果 `8080` 被占用，可以换端口：

```bash
PORT=8090 go run .
# 或
go run . --port 8090
# 或指定完整监听地址
go run . --addr 127.0.0.1:8090
```

然后打开 `http://localhost:8090`。

默认监听地址是 `127.0.0.1:8080`，适合放在 Nginx/Caddy 反向代理后面。容器部署时可以设置 `ADDR=0.0.0.0:8080`。

## 开发模式

```bash
go run .
cd frontend
npm run dev
```

开发模式前端默认把 `/api` 代理到 `http://localhost:8080`。如果后端换端口：

```bash
PORT=8090 go run .
cd frontend
VITE_API_PROXY_TARGET=http://localhost:8090 npm run dev
```

也可以用 `VITE_API_BASE=http://localhost:8090 npm run dev` 让浏览器直接访问后端 API。
直接跨域访问后端时，需要后端允许开发源：

```bash
ALLOWED_ORIGINS=http://localhost:5173 PORT=8090 go run .
cd frontend
VITE_API_BASE=http://localhost:8090 npm run dev
```

## API

- `POST /api/games`
- `GET /api/games?limit=20`（加 `&owner=<id>` 只看某个匿名归属的对局）
- `GET /api/games/{gameId}`
- `POST /api/games/{gameId}/agent/join`
- `POST /api/games/{gameId}/agent/status`
- `POST /api/games/{gameId}/moves`
- `POST /api/games/{gameId}/resign`
- `GET /api/health`

创建人机局：

```json
{ "mode": "human-agent", "humanColor": "black", "forbidden": false, "agentStrategy": "think" }
```

创建机机局：

```json
{ "mode": "agent-agent", "forbidden": false, "agentStrategy": "think" }
```

`mode` 默认是 `human-agent`，人机局会返回 `humanToken` 和 `agentToken`；机机局会返回 `agentBlackToken` 和 `agentWhiteToken`。

创建对局时可选字段：

- `forbidden`（默认 `false`）：开启后仅对黑棋启用连珠禁手（严格递归判定），黑棋走出长连（6 子及以上）、四四或三三即判负（`status` 变为 `white_won`、`endReason` 为 `forbidden`）；黑棋先成恰好 5 子仍判胜，白棋不受限。开启禁手且轮到黑棋时，棋局 JSON 会带上 `forbiddenPoints`（`[{row,col,reason}]`，列出当前所有禁手点），供前端标 ✕ 预警、Agent 落子前规避。
- `agentStrategy`（`think` | `script`，默认 `think`）：仅影响前端“复制提示词”生成的内容（逐步思考 / 让 LLM 先写脚本自动对战），随对局持久化。
- 归属标识：创建时通过请求头 `X-Owner-Id: <id>`（或请求体 `owner` 字段）写入匿名归属，配合 `GET /api/games?owner=<id>` 实现“只看我的对局”。该归属仅用于列表筛选，不是访问控制，所有对局仍可凭 `gameId` 公开查看。

落子接口使用 `Authorization: Bearer <token>`，后端通过 token 自动判断调用者身份。
认输接口使用任一方 token 自动判断认输者；Agent 加入和思考状态接口使用对应 Agent token。机机局中 `nextTurn` 会是 `agent_black` 或 `agent_white`。

## 部署

按你的环境选一种形态（两种都只运行一个后端实例，SQLite 放持久化目录）：

- **方式 A：公网 VPS + Nginx 反代** —— 有公网 IP，Nginx 做 HTTPS / 限流 / 反代。
- **方式 B：家用 Mac mini + Cloudflare Tunnel** —— 无公网 IP、不开任何入站端口，Cloudflare 负责 HTTPS / DDoS / 边缘限流（推荐家用）。

`deploy/nginx.conf.example` 只用于方式 A；方式 B 不需要 Nginx。

### 方式 A：公网 VPS + Nginx 反代

#### Docker Compose（推荐）

```bash
docker compose up -d --build
```

`docker-compose.yml` 已经把端口绑定到 `127.0.0.1`、挂载持久化卷、设置 `restart: unless-stopped` 和 30s 优雅停机窗口，并打开 `TRUST_PROXY_HEADERS=true`（默认部署在反代后面）。

#### Docker（手动）

```bash
docker build -t gomoku .
docker run -d --name gomoku \
  -p 127.0.0.1:8080:8080 \
  -v gomoku-data:/var/lib/gomoku \
  -e ADDR=0.0.0.0:8080 \
  -e DB_PATH=/var/lib/gomoku/wuziqi.db \
  -e STATIC_DIR=/app/frontend/dist \
  -e TRUST_PROXY_HEADERS=true \
  gomoku
```

把 `deploy/nginx.conf.example` 复制到服务器的 Nginx 配置中，替换 `gomoku.example.com` 和证书路径，然后让公网只访问 HTTPS 反代，不要直接暴露 Go 服务端口。服务收到 `SIGTERM`/`SIGINT` 会优雅停机：先停止接收新请求、排空在途请求（最多 20s），并在退出前对 SQLite 做一次 WAL checkpoint。

### 方式 B：家用 Mac mini + Cloudflare Tunnel

Cloudflare Tunnel 由 `cloudflared` 主动向外拨号，**不需要公网 IP，也不在路由器上开任何端口**；HTTPS、HSTS、DDoS、边缘限流都在 Cloudflare 边缘完成，源站只监听本地端口（由 plist 配置，默认 `127.0.0.1:8090`），外部唯一入口就是隧道。

**1. 一键部署**

在仓库根目录用**普通用户**运行（需要 root 的步骤脚本会自动 `sudo`）：

```bash
./deploy/install-macos.sh
```

脚本会：构建前端与 Go 二进制 → 安装到 `/usr/local/{bin,share,var}` → 装并加载两个 launchd 服务（服务本体 + 每日备份）→ 做健康检查并打印实际端口和管理命令。可重复运行（就地重建并重载）。

> 端口、路径、环境变量都从 `deploy/com.gomoku.server.plist` 读取——想改端口就改那里的 `ADDR`（当前 `8090`），脚本会自动跟随。服务自带优雅停机：launchd 停止时发 `SIGTERM`，排空在途请求并对 SQLite 做一次 WAL checkpoint。

管理命令：

```bash
sudo launchctl kickstart -k system/com.gomoku.server   # 重启
sudo launchctl bootout    system/com.gomoku.server     # 停止（优雅）
tail -f /usr/local/var/log/gomoku.log                  # 日志
```

**2. 开 Cloudflare Tunnel（网页托管）**

在 Cloudflare **Zero Trust 仪表盘 → Networks → Tunnels → Create a tunnel → Cloudflared**，给隧道起名后复制它给出的安装命令，在 Mac mini 上运行（形如）：

```bash
sudo cloudflared service install <CONNECTOR-TOKEN>
```

然后在该隧道的 **Public Hostnames** 里加一条：`gomoku.example.com` → 类型 `HTTP`、URL `localhost:8090`（端口用脚本结尾打印的值；DNS 记录会自动创建）。ingress 由网页托管，本机无需 `config.yml`，cloudflared 也会装成开机自启的 launchd 守护。

**3. Cloudflare 仪表盘与 Mac 设置**

- SSL/TLS → 打开 **Always Use HTTPS** 和 **HSTS**（边缘做；app 保持 loopback HTTP 即可）。
- 限流按真实访客 IP：plist 已设 `TRUST_PROXY_HEADERS=true`，服务会优先读 **`CF-Connecting-IP`**（隧道下源站不可直连，该头可信）。
- 可选：**Rate Limiting Rules** 边缘限流；**Cloudflare Access** 给公开观战列表加一层登录。
- **防睡眠**：`sudo pmset -a sleep 0 disablesleep 1`（Mac 睡眠会断隧道）。
- 不需要开任何入站端口或路由器转发，macOS 防火墙也不会弹窗。

### 生产配置

可用环境变量（两种方式通用）：

- `ADDR`：监听地址。公网单机反代建议 `127.0.0.1:8080`，Docker 内部使用 `0.0.0.0:8080`。
- `DB_PATH`：SQLite 路径。生产建议 `/var/lib/gomoku/wuziqi.db`，并挂载持久化卷。
- `STATIC_DIR`：前端构建目录。
- `ALLOWED_ORIGINS`：逗号分隔的 CORS 白名单。留空表示同源部署，不返回跨域放行头。
- `MAX_JSON_BODY_BYTES`：JSON 请求体上限，默认 `16384`。
- `RATE_LIMIT_ENABLED`：是否启用后端每 IP 限速，默认 `true`。
- `RATE_LIMIT_REQUESTS`：每个窗口允许的请求数，默认 `300`。
- `RATE_LIMIT_WINDOW_SECONDS`：限速窗口秒数，默认 `60`。
- `CREATE_RATE_LIMIT_REQUESTS`：创建对局接口（`POST /api/games`）的更严格每 IP 配额，默认 `10`，用于抑制匿名刷局。
- `CREATE_RATE_LIMIT_WINDOW_SECONDS`：创建对局限速窗口秒数，默认 `60`。
- `TRUST_PROXY_HEADERS`：是否信任 `X-Forwarded-For`/`X-Real-IP` 推断客户端 IP，默认 `false`。**仅在可信反代后面才设为 `true`**，否则客户端可伪造该头绕过限速。
- `RETENTION_ENABLED`：是否启用后台对局清理，默认 `true`。
- `RETENTION_FINISHED_HOURS`：已结束对局保留时长（小时），默认 `168`（7 天）。
- `RETENTION_ABANDONED_HOURS`：进行中但长期无活动（被放弃）对局的保留时长，默认 `24`。
- `RETENTION_INTERVAL_MINUTES`：清理任务运行间隔（分钟），默认 `30`。
- `LOG_LEVEL`：结构化日志级别 `debug`/`info`/`warn`/`error`，默认 `info`（JSON 输出到 stdout）。

### 数据和备份

SQLite 适合当前轻量部署，但只建议运行一个后端实例（连接池上限为 1）。WAL 模式会生成 `wuziqi.db-wal` 和 `wuziqi.db-shm`，不要只拷贝单个主库文件做热备。后台清理任务会按 `RETENTION_*` 配置定期删除超期对局，控制数据库长期增长。

推荐用脚本做在线热备（`sqlite3 .backup` 在服务运行时也能得到一致快照）：

```bash
DB_PATH=/var/lib/gomoku/wuziqi.db BACKUP_DIR=/var/backups/gomoku deploy/backup.sh
```

脚本会做完整性校验、gzip 压缩并按 `KEEP`（默认 14）滚动保留。注意：Docker 运行时镜像不含 `sqlite3` CLI，方式 A 的备份要在**宿主机**上对挂载卷执行。定时任务：方式 A（Linux）用 `deploy/gomoku-backup.service` + `deploy/gomoku-backup.timer`（systemd）；方式 B（macOS，自带 `sqlite3`）用 `deploy/com.gomoku.backup.plist`（launchd，见上）。

恢复时停止服务，把（解压后的）备份文件放回 `DB_PATH`，再启动服务。

### 运维与监控

- **探活**：用外部 uptime 监控定期请求 `GET /api/health`。该接口现在会真正 ping 数据库，DB 不可用时返回 `503 {"status":"degraded"}`，可据此告警。容器内置 `HEALTHCHECK` 也打这个端点。
- **磁盘**：对数据卷（`/var/lib/gomoku`）和备份目录设置使用率告警，避免被异常增长撑爆。
- **日志**：服务输出 JSON 结构化日志到 stdout（含每次请求的 method/path/status/耗时/IP），用 `docker logs` 或日志采集器收集；配合 Nginx access/error log 排障。
- **CI**：`.github/workflows/ci.yml` 在 push/PR 时运行 `go vet`、`go test -race`、`govulncheck`、前端构建，以及 `docker build` + Trivy 镜像扫描。

### 安全注意事项

- 必须使用 HTTPS。棋局 token 会保存在浏览器 `localStorage`，也会出现在复制给 Agent 的提示词里；token 等同于本局控制权，泄露后别人可以代替玩家落子或认输。
- 当前最近棋局列表和棋盘读取是公开观战模型。需要私人对局时，应另行改造访问模型。
- 不要提交或发布 `data/`、`frontend/dist/`、`frontend/node_modules/`、`frontend/tsconfig.tsbuildinfo`；这些已经在 `.gitignore` / `.dockerignore` 中排除。
