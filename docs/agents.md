# 自定义 Agent

Knack 内置读取 Claude、Codex 和 Factory 的本地会话日志。其他 Agent 有两种接入方式，都能进入首页统计、全部会话、轮次回放和 Agent 用量统计：

- **字段映射**：日志是 JSON Lines（每行一个 JSON 记录）时，在 `~/.config/skill-manager/agents.json` 中声明字段路径。
- **[JS 插件](#js-插件)**：日志是任意格式（纯文本、整个 JSON 文件、SQLite……）或需要计算时，用一个 JS 文件读取。

自定义 Agent 和内置 Agent 遵守同样的约束：日志只读，不联网，缓存只保存统计元信息，回放内容只在打开会话时读取到内存中。字段映射不执行任何代码；插件可以运行任意 JavaScript，但只能通过 Knack 暴露的只读方法接触本机。

修改 `agents.json` 或插件后需要重启 Knack。修改某个 Agent 的映射或脚本后，只重建这个 Agent 的统计缓存。

## 字段映射示例

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

## JS 插件

把脚本放到 `~/.config/skill-manager/plugins/` 下，一个 `.js` 文件定义一个 Agent，不需要其他配置。脚本调用 `knack.registerAgent` 注册自己，提供两个函数：`list` 列出会话，`read` 把一个会话转成[标准记录](#标准记录)。

读取 `时间戳|角色|正文` 格式的纯文本日志：

```js
knack.registerAgent({
  id: "my-agent",
  name: "My Agent",
  list() {
    return knack.walk("~/.my-agent/logs").filter((f) => f.name.endsWith(".log"));
  },
  read(session) {
    return knack.readText(session.path).trim().split("\n").map((line) => {
      const [time, role, text] = line.split("|");
      return { time: Number(time), role, text };
    });
  },
});
```

一个 SQLite 文件保存多个会话：

```js
const db = "~/.my-agent/state.db";

knack.registerAgent({
  id: "my-db-agent",
  name: "My DB Agent",
  list() {
    return knack
      .query(db, "SELECT chat, max(at) AS last FROM messages GROUP BY chat")
      .map((c) => ({ path: db, id: c.chat, version: c.last }));
  },
  read(session) {
    return knack
      .query(db, "SELECT at, role, body FROM messages WHERE chat = ? ORDER BY at", session.id)
      .map((m) => ({ time: m.at, role: m.role, text: m.body }));
  },
});
```

### `list` 返回的会话

`list` 返回数组，每一项会原样传给 `read`，可以附带任意字段。Knack 读取其中三个：

| 字段 | 必填 | 说明 |
| --- | --- | --- |
| `path` | 是 | 会话所在的文件，绝对路径或 `~/` 开头，文件必须存在，且不能属于其他 Agent。`knack.walk` 返回的对象可以直接使用 |
| `id` | 否 | 会话 ID。一个文件有多个会话时必填；缺省时取文件名 |
| `version` | 否 | 内容变化时要跟着变的值，缺省时取文件的修改时间和大小。一个文件有多个会话时建议提供，这样只重读变化的会话 |

### 标准记录

`read` 返回记录数组，每条记录的字段：

| 字段 | 必填 | 说明 |
| --- | --- | --- |
| `time` | 是 | RFC 3339 文本，或 Unix 秒 / 毫秒数字 |
| `role` | 是 | `user`、`assistant` 或 `tool`；其他值的记录只提供会话元信息 |
| `text` | 否 | 正文：字符串，或 `{type: "text", text}` 块组成的列表 |
| `id` | 否 | 记录 ID；同一 ID 只计一次 |
| `requestId` | 否 | 同一请求的多条记录合并为一次请求，缺省时用 `id` |
| `model`、`thinking` | 否 | 模型名、思考文本 |
| `usage` | 否 | `{input, output, cacheRead, cacheWrite}`；`input` 不包含缓存 |
| `toolCalls` | 否 | assistant 记录发起的调用：`[{id, name, input}]` |
| `toolResults` | 否 | `role` 为 `tool` 的记录携带的结果：`[{id, output, error}]`，`error` 是布尔值 |
| `sessionId`、`title`、`cwd`、`branch` | 否 | 会话元信息 |

### Knack 暴露的方法

| 方法 | 说明 |
| --- | --- |
| `knack.home` | 主目录 |
| `knack.readText(path)` | 读取文件文本，`.zst` 会先解压，上限 64 MiB |
| `knack.stat(path)` | 返回 `{path, name, size, modTime, isDir}`，不存在时返回 `null`。`modTime` 是毫秒 |
| `knack.walk(dir)` | 递归列出目录下的普通文件，元素同 `stat`，不跟随符号链接，最多 20000 个；目录不存在时返回空数组 |
| `knack.query(db, sql, ...params)` | 在只读连接上执行一条 `SELECT`（可带 `WITH`），返回行对象数组，最多 1048576 行。只能有一条语句，结尾以外不能出现分号，含分号的文本请用参数传入。数据库本身不会被修改，但读取 WAL 模式的数据库时，SQLite 会在旁边创建 `-shm` 和 `-wal` 文件 |
| `knack.registerAgent(def)` | 注册 Agent，每个文件调用一次 |
| `console.log` 等 | 输出到 Knack 的标准错误，从终端启动时可以看到 |

路径必须是绝对路径或以 `~/` 开头，可以指向主目录以外。

### 运行规则

- 每个插件运行在自己的 worker 进程里（Knack 程序的另一份实例），脚本崩溃或卡死都不会让 Knack 退出，worker 也随 Knack 退出而结束。
- 脚本可以运行任意 JavaScript，但只有上面这些方法能接触本机：没有 `require`、`fetch`、定时器，也没有写文件、联网或启动进程的方法。安装第三方插件前仍应读一遍：它能读取你有权读取的所有文件，并显示在 Knack 里。
- 代码同步执行，`list` 和 `read` 不能返回 Promise。
- 每个插件单线程执行，全局变量在多次调用间保留，可以用来做缓存；worker 被重启后清空，统计不能依赖它。
- 会话的 `version` 变化时整个会话重读，没有按字节增量读取。列表每次刷新都会调用，应保持轻量。
- 加载限时 5 秒，每次 `list` 或 `read` 限时 30 秒，单次结果上限 128 MiB。超时或超过内存上限时 worker 被结束，下次调用自动启动新的。`list` 或 `read` 连续超时或崩溃 3 次后插件停用，重启 Knack 后恢复。
- 内存上限：JavaScript 堆 1 GiB。SQLite 在这之外分配内存，单个值上限 64 MiB，整体没有硬性上限，只受调用时限约束。
- 一次刷新中，一个插件的 `read` 累计超过 10 秒后，其余会话留到下一次刷新，统计会分批出现。
- `read` 失败时保留上次的统计，并在警告中显示错误；同一 `version` 不再重试，数据变化或重启后才会再读。`list` 失败时沿用上次列出的会话。

## 统计规则

- 有正文的 user 记录开始新的一轮；只带工具结果的记录不算新的一轮。
- 每条 assistant 记录是一次请求。请求耗时按上一条带时间的记录到这条记录的间隔估算，tok/s 由此估算。
- 工具调用耗时按调用记录到结果记录的间隔估算。
- 没有工具调用、带正文的 assistant 记录结束本轮；到下一条 user 记录之间算作等待你回复。
- 缺少用量字段的请求不按零计，会显示“暂不支持”。
- Skill 调用、思考耗时、权限等待和子 Agent 关联暂不支持。

## 排错

配置或插件有问题时，首页底部会提示“自定义 Agent 配置有 N 个问题”，悬停可以看到具体原因。有问题的 Agent 不会加载，其他 Agent 不受影响。

如果目录下有日志，但首页提示“没有可识别的会话记录”，通常是 `time` 或 `role` 的路径不对，或者 `roles` 没有包含日志里实际的角色值。
