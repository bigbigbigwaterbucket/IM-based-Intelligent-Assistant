# Project Rules

## Feishu OpenAPI Usage

- Backend Go code must call Feishu/Lark OpenAPI through `github.com/larksuite/oapi-sdk-go/v3`.
- Do not invoke `lark-cli` from backend runtime code for Feishu API operations.
- Do not add ad hoc raw HTTP wrappers for Feishu APIs when the SDK exposes the endpoint.
- If the SDK does not expose a needed Feishu API, keep the fallback local or document the gap explicitly before adding another integration path.

TODO：agent管理整个调用链路、用户上下文session持久化，
使用new命令可重置上下文、使用skill优化doc文档与ppt文档产出
文档、ppt的修改/迭代能力、意图分析能力、step动态增删
codex resume 019ddeee-2f64-7ef1-9f94-747baf9bf9f4