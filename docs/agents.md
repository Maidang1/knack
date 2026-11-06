# 自定义 Agent

Knack 内置读取 Claude、Codex 和 Factory 的本地会话日志。其他 Agent 只要把会话写成 JSON Lines（每行一个 JSON 记录），就可以在 `~/.config/skill-manager/agents.json` 中声明字段映射，让它进入会话总览、会话列表、轮次回放和 Agent 用量统计。

映射只用于选择字段，不执行任何代码。自定义 Agent 和内置 Agent 遵守同样的约束：日志只读打开，不联网，缓存只保存统计元信息，回放内容只在打开会话时读取到内存中。

修改 `agents.json` 后需要重启 Knack。修改某个 Agent 的映射后，只重建这个 Agent 的统计缓存。

## 示例

下面是一个 OpenAI 消息格式的日志：

```jsonl
{"ts":1793491200000,"id":"m1","session":"chat-1","dir":"/work/demo","role":"system","content":"You are helpful."}
{"ts":1793491200000,"id":"m2","role":"user","content":"列出文件"}
{"ts":1793491203000,"id":"m3","role":"assistant","model":"gpt-x","content":"","tool_calls":[{"id":"call-1","type":"function","function":{"name":"shell","arguments":"{\"cmd\":\"ls\"}"}}],"usage":{"prompt_tokens":120,"completion_tokens":30,"cached_tokens":20}}
{"ts":1793491205000,"id":"m4","role":"tool","tool_call_id":"call-1","content":"README.md\nmain.go"}
{"ts":1793491208000,"id":"m5","role":"assistant","model":"gpt-x","content":"有 README.md 和 main.go。","usage":{"prompt_tokens":200,"completion_tokens":15,"cached_tokens":100}}
```

对应的配置：

```json
{
  "agents": [
    {
      "id": "my-agent",
      "name": "My Agent",
      "roots": ["~/.my-agent/logs"],
      "fields": {
        "time": "ts",
        "role": "role",
        "text": "content",
        "id": "id",
        "model": "model",
        "sessionId": "session",
        "cwd": "dir",
        "usage": {
          "input": "usage.prompt_tokens",
          "output": "usage.completion_tokens",
          "cacheRead": "usage.cached_tokens",
          "inputIncludesCache": true
        },
        "toolCalls": {
          "path": "tool_calls",
          "match": { "type": "function" },
          "id": "id",
          "name": "function.name",
          "input": "function.arguments"
        },
        "toolResults": { "id": "tool_call_id", "output": "content" }
      }
    }
  ]
}
```

Anthropic 消息格式的日志把工具调用和结果放在 `content` 块里，可以这样映射：

```json
"toolCalls":   { "path": "message.content", "match": { "type": "tool_use" },    "id": "id",          "name": "name", "input": "input" },
"toolResults": { "path": "message.content", "match": { "type": "tool_result" }, "id": "tool_use_id", "output": "content", "error": "is_error" }
```

## 字段

| 字段 | 必填 | 说明 |
| --- | --- | --- |
| `id` | 是 | 小写字母、数字、`-` 和 `_`，最长 32 个字符，不能与内置 Agent 重复 |
| `name` | 是 | 显示名称，最长 40 个字符 |
| `roots` | 是 | 主目录下的日志目录，可以带 `~/`。不能指向主目录以外，也不能与其他 Agent 的目录重叠 |
| `suffixes` | 否 | 默认 `[".jsonl"]`；也支持 `.jsonl.zst`，会先解压 |
| `fields.time` | 是 | RFC 3339 时间文本，或 Unix 秒 / 毫秒数字 |
| `fields.role` | 是 | 记录的角色 |
| `fields.text` | 是 | 正文：字符串，或 `{"type": "text", "text": …}` 块组成的列表 |
| `fields.id` | 否 | 记录 ID；同一 ID 只计一次，避免重复记录重复计数 |
| `fields.request` | 否 | 请求 ID；同一请求的多条记录合并为一次请求。缺省时用记录 ID |
| `fields.model`、`thinking` | 否 | 模型名、思考文本 |
| `fields.sessionId`、`title`、`cwd`、`branch` | 否 | 会话元信息；缺省时会话 ID 取文件名 |
| `fields.usage` | 否 | `input`、`output`、`cacheRead`、`cacheWrite`。输入 Token 包含缓存读写时设 `inputIncludesCache` |
| `fields.toolCalls` | 否 | `path` 指向列表（或单个对象）；`match` 只保留字段相等的项；`id`、`name`、`input` 相对每一项 |
| `fields.toolResults` | 否 | 同上，`output` 和 `error` 相对每一项。不设 `path` 时，tool 角色的整条记录就是一条结果 |
| `roles` | 否 | `user`、`assistant`、`tool` 各自对应的角色值，默认分别是 `user`/`human`、`assistant`/`ai`/`model`、`tool` |

字段路径用 `.` 访问嵌套对象，例如 `message.usage.input_tokens`。

## 统计规则

- 有正文的 user 记录开始新的一轮；只带工具结果的记录不算新的一轮。
- 每条 assistant 记录是一次请求。请求耗时按上一条带时间的记录到这条记录的间隔估算，tok/s 由此估算。
- 工具调用耗时按调用记录到结果记录的间隔估算。
- 没有工具调用、带正文的 assistant 记录结束本轮；到下一条 user 记录之间算作等待你回复。
- 缺少用量字段的请求不按零计，会显示“暂不支持”。
- Skill 调用、思考耗时、权限等待和子 Agent 关联暂不支持。

## 排错

配置有问题时，总览底部会提示“自定义 Agent 配置有 N 个问题”，悬停可以看到具体原因。有问题的 Agent 不会加载，其他 Agent 不受影响。

如果目录下有日志，但总览提示“没有可识别的会话记录”，通常是 `time` 或 `role` 的路径不对，或者 `roles` 没有包含日志里实际的角色值。
