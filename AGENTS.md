# Project Rules

## Feishu OpenAPI Usage

- Backend Go code must call Feishu/Lark OpenAPI through `github.com/larksuite/oapi-sdk-go/v3`.
- Do not invoke `lark-cli` from backend runtime code for Feishu API operations.
- Do not add ad hoc raw HTTP wrappers for Feishu APIs when the SDK exposes the endpoint.
- If the SDK does not expose a needed Feishu API, keep the fallback local or document the gap explicitly before adding another integration path.

TODO：agent管理整个调用链路、用户上下文session持久化，
使用new命令可重置上下文、使用skill优化doc文档与ppt文档产出
文档、ppt的修改/迭代能力（一次task任务可多次修改）、意图分析能力、step动态增删
启动任务时，发送web dashboard地址方便用户查看进度
使用飞书卡片优化bot使用引导？任务task超一定时间没有后续就发卡片让用户主动结束任务
关于task绑定会话/用户的范围需要仔细思考，如果绑定单个用户，那么协作性较差。如果绑定会话，那么容易混
建议task根据发起者id进行绑定？参考xx
/assistant (new) 输入体验不好的问题
DAG、multi-agent并行完成文档/ppt生成，redis优化
删掉启发式文档生成，在orchestrator中
review是必要的，经常发现ai写的代码不符合实际要求，例如群聊消息没进入agent上下文/任务是更新doc文档但agent没读已有的doc，不review不好看出问题
TODO: buildRevisionPlan使用LLM planner而不是启发式程序planner

卡片点击end task报错200340
end task卡片点击后不可再点优化
https://open.feishu.cn/document/feishu-cards/card-callback-communication#c98c3220

客户端页面
codex resume 019ddeee-2f64-7ef1-9f94-747baf9bf9f4
任务持续性+文档更新注入+乱码
codex resume 019de31c-f844-7f41-84c9-9da25f9c0204