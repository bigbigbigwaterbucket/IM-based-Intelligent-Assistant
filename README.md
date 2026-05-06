# Agent Pilot MVP

Monorepo for the initial desktop + backend + mobile H5 implementation of the IM-based office assistant.

## Structure

- `backend`: Go API server, orchestrator, task state hub, and tool runners
- `apps/desktop`: Electron + React desktop workbench
- `apps/mobile`: React H5 mobile task board
- `apps/mobile-rn`: Expo React Native mobile task board
- `packages/shared`: Shared TypeScript task/event contracts

## Current MVP

- Manual task creation from desktop
- Go orchestrator with task lifecycle and retry flow
- WebSocket state sync to desktop and mobile
- Heuristic-first planner with optional Eino-backed LLM planning
- Optional Feishu Docx tool runner via Go SDK

## Development

### Backend

```powershell
cd backend
go run ./cmd/server
```

### Frontend

```powershell
npm install
npm run dev:desktop
npm run dev:mobile
npm run dev:mobile-rn
npm run desktop -w @agent-pilot/desktop
```

For React Native on a real device or emulator, configure the backend with a LAN-reachable host instead of `localhost`:

```powershell
$env:EXPO_PUBLIC_API_BASE="http://10.0.2.2:8080"
$env:EXPO_PUBLIC_WS_BASE="ws://10.0.2.2:8080"
npm run dev:mobile-rn
```

## Environment

Backend supports these optional variables:

- `PORT`: HTTP port, default `8080`
- `DATABASE_URL`: SQLite DSN, default `file:agentpilot.db?_pragma=busy_timeout(5000)`
- `ARK_API_KEY` / `ARK_BASE_URL` / `ARK_MODEL`: preferred planner model config
- `DEEPSEEK_API_KEY` / `DEEPSEEK_BASE_URL` / `DEEPSEEK_MODEL`: fallback planner model config
- `AGENT_SKILL_DIR`: Eino skill directory, default `backend/skills` from repo root or `skills` from the backend directory
- `ENABLE_FEISHU_TOOLS`: set to `true` to create real Feishu Docx artifacts via Go SDK
- `FEISHU_APP_ID` / `FEISHU_APP_SECRET` / `FEISHU_DOC_BASE_URL`: Feishu Docx integration config
