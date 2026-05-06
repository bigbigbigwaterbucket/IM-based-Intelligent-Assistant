# Project Rules

## Feishu OpenAPI Usage

- Backend Go code must call Feishu/Lark OpenAPI through `github.com/larksuite/oapi-sdk-go/v3`.
- Do not invoke `lark-cli` from backend runtime code for Feishu API operations.
- Do not add ad hoc raw HTTP wrappers for Feishu APIs when the SDK exposes the endpoint.
- If the SDK does not expose a needed Feishu API, keep the fallback local or document the gap explicitly before adding another integration path.

## LLM / Agent Usage (Eino)
- All LLM and Agent-related logic in backend Go code must be implemented via `github.com/cloudwego/eino` (chat model, agent, tool orchestration, etc.).
- Do not call OpenAI-compatible APIs (DeepSeek, OpenAI, etc.) via raw HTTP in business code.
- Do not create ad hoc wrappers around LLM providers when eino already provides the capability.
- If a required capability is not supported by eino, document the limitation first and isolate any fallback implementation (do not mix raw calls into main business logic).
- Keep LLM invocation logic centralized (e.g., unified model factory / config), avoid scattering model initialization across modules.