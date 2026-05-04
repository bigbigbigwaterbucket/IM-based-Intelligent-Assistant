# Project Rules

## Feishu OpenAPI Usage

- Backend Go code must call Feishu/Lark OpenAPI through `github.com/larksuite/oapi-sdk-go/v3`.
- Do not invoke `lark-cli` from backend runtime code for Feishu API operations.
- Do not add ad hoc raw HTTP wrappers for Feishu APIs when the SDK exposes the endpoint.
- If the SDK does not expose a needed Feishu API, keep the fallback local or document the gap explicitly before adding another integration path.

TODO：agent管理整个调用链路、用户上下文session持久化，
使用new命令可重置上下文、使用skill优化doc文档与ppt文档产出
意图分析能力、step动态增删（如果群聊消息上下文不够，尝试获取更多消息
使用飞书卡片优化bot使用引导？
关于task绑定会话/用户的范围需要仔细思考，如果绑定单个用户，那么协作性较差。如果绑定会话，那么容易混
/assistant (new) 输入体验不好的问题
DAG、multi-agent并行完成文档/ppt生成，redis优化
review是必要的，经常发现ai写的代码不符合实际要求，例如群聊消息没进入agent上下文/任务是更新doc文档但agent没读已有的doc，不review不好看出问题


IM文件信息获取能力，高级ppt生成能力，高级文档生成能力
（以下是素材/以下是我初步定的文件：xxx 这类信息能进入agent上下文参与生成）
对应任务 富媒体与画布操作:支持在文档/画布中通过指令插入和处理图片、表格等，或进行布局调整。

为任务添加Owner（或管理员） 可以：
结束任务、回滚版本，也可以指派给其他人
在启动任务和待审核卡片时展示owner？删掉桌面端/移动端的任务发起框？

主动发起总结功能的两个问题：
1、点击任务后，会删除之前缓存的消息，但是之后用户再发消息，/assistant 修订 不会删除消息
2、切换主题时，不能灵敏开启检测，themeKey重复的问题
3、如果之前的任务没结束，主动检测会开启新任务？？不应该

启动任务时，发送web dashboard地址方便用户查看进度/在线编辑
使用Yjs实现多端CRDT
飞书bot生成的文档可供编辑/ppt文档编辑实现？ 添加同步回系统文件，这样agent才能读到用户的改动

卡片点击end task报错200340
end task卡片点击后不可再点优化
https://open.feishu.cn/document/feishu-cards/card-callback-communication#c98c3220

主动发起总结
To continue this session, run codex resume 019de786-0eb3-76b3-bf3d-427dd3059465