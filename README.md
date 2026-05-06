# Assistant agent- 基于IM的办公协同智能助手

飞书 AI 校园挑战赛参赛作品

## 核心能力

### 1. IM 触发任务

- 支持飞书 Bot 作为任务入口，用户可在群聊或单聊中通过自然语言发起任务。
- 支持读取最近 IM 上下文，过滤 Bot 自身消息，避免把任务启动提示重复写入材料。
- 支持读取消息中的简单文件素材：`.md`、`.txt`、`.docx` 可被解析为文本并进入 Agent 上下文。
- 支持任务启动后返回 Web Dashboard 地址，用户可以实时查看进度和在线编辑文档。
- 支持主动任务检测的基础能力：根据群聊片段判断是否存在可推进的总结、方案、PPT、报告等办公任务。

### 2. Agent 规划与模块化编排

后端将一次办公任务拆成可组合的场景模块：

- `im.fetch_thread`：读取 IM 对话和附件素材。
- `doc.generate` / `doc.update`：生成或更新结构化 Markdown 文档，并可同步创建飞书 Docx。
- `slide.generate` / `slide.regenerate`：将内容整理为 genppt 兼容 Markdown，并生成 PPTX。
- `archive.bundle`：汇总任务产物 manifest，保留文档、PPT、计划和上下文路径。
- `sync.broadcast`：仅同步状态，不生成产物，适合问候或状态查询类任务。

规划器采用启发式兜底 + 可选 LLM 规划。开启 Eino/ADK Agent 后，执行 Agent 会在调用文档或 PPT 工具前加载本地 Skill，例如 `document_generation`、`slide_generation`，让不同产物生成遵循独立的场景规范。

### 3. 文档生成与协同编辑

- 文档首先落盘为本地 Markdown，作为 Agent、桌面端、移动端共同编辑的源文件。
- 开启飞书工具后，后端通过 `github.com/larksuite/oapi-sdk-go/v3` 创建或更新飞书 Docx，并返回云文档链接。
- 桌面端和移动端提供 Markdown 协同编辑器，支持预览、快照保存和导出。
- 使用 Yjs/CRDT 管理多端编辑冲突；后端提供协同文档、增量更新、快照和 WebSocket 广播接口。
- 处理了“前端提前打开 Markdown 抽屉导致空协同记录”的时序问题：当文档生成后，后端可从任务产物或工具调用记录补齐 Markdown 源。

### 4. PPT / 演示稿生成

- 使用 `github.com/CoolBanHub/genppt` 将 Markdown 转换为 PPTX。
- 每次生成会同时保留 PPT Markdown 源文件和 PPTX 文件，便于后续修订、归档和飞书 Bot 回复文件。
- 当前飞书 Go SDK 没有直接创建飞书 Slides 的封装，因此演示稿以本地 PPTX 产物为主。

### 5. 多端驾驶舱

- 桌面端：Electron + React，适合任务创建、进度查看、人工确认和 Markdown 精修。
- 移动 H5：React + Vite，适合轻量查看和协同。
- 移动 RN：Expo React Native，面向 Android/iOS 模拟器或真机。
- 状态同步：任务状态通过后端 WebSocket 广播；Markdown 协同通过独立的 Yjs WebSocket 通道同步。
- 离线体验：后端断开时，前端保留本地编辑状态并提示协同服务不可用；恢复连接后重试连接并同步未确认更新。

## 命题覆盖关系

| 命题场景 | 当前实现 |
| --- | --- |
| 场景 A：意图/指令入口 | 飞书 Bot 文本入口、桌面端手动任务入口、主动检测基础能力 |
| 场景 B：任务理解与规划 | Go Orchestrator、启发式 Planner、可选 Eino/ADK LLM Agent |
| 场景 C：文档/白板生成与编辑 | Markdown 源文件、飞书 Docx、Yjs 多端协同编辑 |
| 场景 D：演示稿生成与修改 | genppt Markdown -> PPTX，支持重新生成 |
| 场景 E：多端协作与一致性 | Electron 桌面端、React H5、Expo RN，WebSocket + Yjs/CRDT |
| 场景 F：总结与交付 | 飞书链接、本地 Markdown/PPTX、manifest 归档、Bot 文件回复 |

## 加分项完成情况

| 加分项 | 完成情况                                                                                                                |
| --- |---------------------------------------------------------------------------------------------------------------------|
| 离线支持 | 已实现基础离线编辑体验。后端断开时，桌面端和 RN 端会提示协同服务不可用，用户仍可在本地编辑 Markdown；恢复连接后通过 WebSocket 重连与 Yjs update 同步未确认修改。                  |
| 高级 Agent 能力 | 已实现主动任务检测能力。后端程序缓存飞书群聊IM消息，先通过规则层，在群聊出现汇报关键词、文档需求、时间要求等信号时触发LLM任务识别，LLM判断为真实任务需求后，再经过冷却层判断任务主题是否重复，最终弹出卡片。          |
| 第三方平台集成 | 已接入飞书开放平台。飞书 Bot 支持 IM 入口、任务启动、进度链接返回、文件回复；后端通过 `github.com/larksuite/oapi-sdk-go/v3` 读取 IM 消息、下载消息资源、创建或更新飞书 Docx。 |

## 技术架构

```text
Feishu IM / Desktop GUI / Mobile RN
        |
        v
Go HTTP API + WebSocket
        |
        +-- Orchestrator：任务生命周期、步骤状态、人工确认
        +-- Planner：启发式规划 + 可选 LLM 规划
        +-- Agent Executor：Eino/ADK 工具调用与 Skill 编排
        +-- Tool Runner：IM、Docx、PPTX、归档工具
        +-- Collab Service：Yjs 更新、快照、Markdown 导出
        +-- SQLite Store：任务、会话、消息缓存、工具调用、协同文档
```

## 目录结构

```text
backend/             Go API Server、Agent 编排、飞书集成、协同服务
backend/skills/      文档生成、PPT 生成等 Eino Skill
apps/desktop/        Electron + React 桌面驾驶舱
apps/mobile/         React H5 移动端
apps/mobile-rn/      Expo React Native 移动端
packages/shared/     前端共享 TypeScript 类型
```

## 快速启动

### 1. 安装依赖

```powershell
npm install
```

Go 后端依赖会在首次运行或测试时由 Go module 自动拉取。

### 2. 启动后端

```powershell
cd backend
go run ./cmd/server
```

默认监听 `http://localhost:8080`，SQLite 数据库默认为 `backend/agentpilot.db` 或当前运行目录下的 `agentpilot.db`。

### 3. 启动桌面端

开发模式：

```powershell
npm run dev:desktop
```

Electron 窗口：

```powershell
npm run desktop -w @agent-pilot/desktop
```

### 4. 启动移动 H5

```powershell
npm run dev:mobile
```

### 5. 启动 React Native 移动端

Android 模拟器访问宿主机后端时，通常使用 `10.0.2.2`：

```powershell
$env:EXPO_PUBLIC_API_BASE="http://10.0.2.2:8080"
$env:EXPO_PUBLIC_WS_BASE="ws://10.0.2.2:8080"
npm run android:mobile-rn
```

真机需要把地址改成电脑在局域网中的 IP，例如：

```powershell
$env:EXPO_PUBLIC_API_BASE="http://192.168.1.20:8080"
$env:EXPO_PUBLIC_WS_BASE="ws://192.168.1.20:8080"
npm run dev:mobile-rn
```

## 环境变量

后端会按顺序加载 `ENV_FILE`、`backend/.env`、`.env`。已经在系统环境中设置的变量不会被 `.env` 覆盖。

### 基础配置

| 变量 | 说明 | 默认值 |
| --- | --- | --- |
| `PORT` | 后端 HTTP 端口 | `8080` |
| `DATABASE_URL` | SQLite DSN | `file:agentpilot.db?_pragma=busy_timeout(5000)` |
| `ARTIFACT_DIR` | Markdown、PPTX、manifest 等产物目录 | `data/pilot_artifacts` |
| `ENV_FILE` | 自定义 `.env` 文件路径 | 空 |

### Agent / LLM 配置

| 变量 | 说明 |
| --- | --- |
| `DEEPSEEK_API_KEY` | Planner / Agent 默认 LLM Key |
| `DEEPSEEK_BASE_URL` | DeepSeek 兼容 OpenAI API 地址，默认 `https://api.deepseek.com` |
| `DEEPSEEK_MODEL` | 默认模型，未配置时为 `deepseek-chat` |
| `AGENT_EXECUTOR` | 设置为 `adk`、`eino-adk`、`react` 或 `eino-react` 启用执行 Agent |
| `ENABLE_ADK_AGENT` / `ENABLE_REACT_AGENT` | 设置为 `true` 或 `1` 启用执行 Agent |
| `AGENT_API_KEY` / `AGENT_BASE_URL` / `AGENT_MODEL` | 覆盖执行 Agent 使用的模型配置 |
| `AGENT_SKILL_DIR` | Skill 目录，默认自动查找 `backend/skills` 或 `skills` |

### 飞书集成

| 变量 | 说明 |
| --- | --- |
| `ENABLE_FEISHU_BOT` | 启用飞书 Bot 事件入口 |
| `FEISHU_APP_ID` / `FEISHU_APP_SECRET` | 飞书应用凭据 |
| `FEISHU_VERIFICATION_TOKEN` | 飞书事件订阅校验 token |
| `FEISHU_EVENT_ENCRYPT_KEY` | 飞书事件加密 key，可选 |
| `PUBLIC_BASE_URL` | 对飞书用户可访问的 Web Dashboard 公网地址 |
| `ENABLE_FEISHU_TOOLS` / `ENABLE_LARK_TOOLS` | 启用飞书 Docx 创建/更新工具 |
| `FEISHU_DOC_BASE_URL` | 飞书文档链接域名，例如 `https://example.feishu.cn` |

后端飞书 OpenAPI 调用遵循项目约束：运行时代码使用 `github.com/larksuite/oapi-sdk-go/v3`，不通过 `lark-cli` 或临时 raw HTTP wrapper 调用。

### 主动检测

| 变量 | 说明 | 默认值 |
| --- | --- | --- |
| `ENABLE_PROACTIVE_DETECTION` | 启用群聊主动任务检测 | 关闭 |
| `PROACTIVE_RULE_THRESHOLD` | 规则命中阈值 | `0.40` |
| `PROACTIVE_LLM_CONFIDENCE` | LLM 判断置信度阈值 | `0.55` |
| `PROACTIVE_COOLDOWN_SECONDS` | 同主题任务冷却时间 | `3600` |
| `PROACTIVE_CACHE_LIMIT` | 每个会话缓存的最近消息数 | `30` |

### 前端配置

桌面端和移动 H5：

| 变量 | 说明 | 默认值 |
| --- | --- | --- |
| `VITE_API_BASE` | 后端 HTTP 地址 | `http://localhost:8080` |
| `VITE_WS_BASE` | 后端 WebSocket 地址 | `ws://localhost:8080` |

React Native：

| 变量 | 说明 |
| --- | --- |
| `EXPO_PUBLIC_API_BASE` | 后端 HTTP 地址，模拟器常用 `http://10.0.2.2:8080` |
| `EXPO_PUBLIC_WS_BASE` | 后端 WebSocket 地址，模拟器常用 `ws://10.0.2.2:8080` |

## 常用命令

```powershell
# 后端测试
cd backend
go test ./...

# 前端类型检查
cd ..
npm run typecheck

# 构建桌面端 / 移动 H5
npm run build:desktop
npm run build:mobile
```

## 一句话演示路径

1. 在飞书群聊中发送 `/assistant 总结最近讨论并生成方案文档和汇报 PPT`。
2. Bot 创建任务并返回 Dashboard 链接。
3. Agent 读取群聊上下文和简单附件，规划文档与 PPT 步骤。
4. 后端生成 Markdown、飞书 Docx、PPTX，并在桌面端/移动端实时同步任务状态。
5. 用户在桌面端或 RN 移动端协同修改 Markdown，断线后也可继续编辑，恢复连接后同步。
6. 用户确认后，通过飞书链接、本地 PPTX 和 manifest 完成交付归档。
