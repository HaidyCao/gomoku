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

## API

- `POST /api/games`
- `GET /api/games?limit=20`
- `GET /api/games/{gameId}`
- `POST /api/games/{gameId}/agent/join`
- `POST /api/games/{gameId}/agent/status`
- `POST /api/games/{gameId}/moves`
- `POST /api/games/{gameId}/resign`
- `GET /api/health`

创建人机局：

```json
{ "mode": "human-agent", "humanColor": "black" }
```

创建机机局：

```json
{ "mode": "agent-agent" }
```

`mode` 默认是 `human-agent`，人机局会返回 `humanToken` 和 `agentToken`；机机局会返回 `agentBlackToken` 和 `agentWhiteToken`。

落子接口使用 `Authorization: Bearer <token>`，后端通过 token 自动判断调用者身份。
认输接口使用任一方 token 自动判断认输者；Agent 加入和思考状态接口使用对应 Agent token。机机局中 `nextTurn` 会是 `agent_black` 或 `agent_white`。
