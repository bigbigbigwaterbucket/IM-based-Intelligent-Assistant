# Project Rules

## Feishu OpenAPI Usage

- Backend Go code must call Feishu/Lark OpenAPI through `github.com/larksuite/oapi-sdk-go/v3`.
- Do not invoke `lark-cli` from backend runtime code for Feishu API operations.
- Do not add ad hoc raw HTTP wrappers for Feishu APIs when the SDK exposes the endpoint.
- If the SDK does not expose a needed Feishu API, keep the fallback local or document the gap explicitly before adding another integration path.
