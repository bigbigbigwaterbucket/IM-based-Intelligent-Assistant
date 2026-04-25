# Agent Pilot MVP

Monorepo for the initial desktop + backend + mobile H5 implementation of the IM-based office assistant.

## Structure

- `backend`: Go API server, orchestrator, task state hub, and tool runners
- `apps/desktop`: Electron + React desktop workbench
- `apps/mobile`: React H5 mobile task board
- `packages/shared`: Shared TypeScript task/event contracts

## Current MVP

- Manual task creation from desktop
- Go orchestrator with task lifecycle and retry flow
- WebSocket state sync to desktop and mobile
- Placeholder planner and optional Lark Docs/Slides tool runner

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
npm run desktop -w @agent-pilot/desktop
```

## Environment

Backend supports these optional variables:

- `PORT`: HTTP port, default `8080`
- `DATABASE_URL`: SQLite DSN, default `file:agentpilot.db?_pragma=busy_timeout(5000)`
- `OPENAI_API_KEY`: enables real planner calls if implemented later
- `LARK_CLI_PATH`: override `lark-cli` executable path
- `ENABLE_LARK_TOOLS`: set to `true` to run real `lark-cli` docs/slides commands
