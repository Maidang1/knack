# 自定义 Agent

Knack 随程序嵌入 Claude、Codex 的 JS 插件，Factory 仍由内建 Go format 读取。三者无需用户安装插件，也不依赖 `~/.config/skill-manager/plugins`。内建 Agent 的 id、name 和日志根目录始终保留，用户配置或插件不能覆盖、认领（包括通过符号链接）。其他 Agent 有两种接入方式，都能进入首页统计、全部会话、轮次回放和 Agent 用量统计：

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

用户插件把脚本放到 `~/.config/skill-manager/plugins/` 下，一个 `.js` 文件定义一个 Agent，不需要其他配置。脚本调用 `knack.registerAgent` 注册自己，提供两个函数：`list` 列出会话，`read` 把一个会话转成[标准记录](#标准记录)。

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

### 用户插件运行规则

- 每个插件运行在自己的 worker 进程里（Knack 程序的另一份实例），脚本崩溃或卡死都不会让 Knack 退出，worker 也随 Knack 退出而结束。
- 脚本可以运行任意 JavaScript，但只有上面这些方法能接触本机：没有 `require`、`fetch`、定时器，也没有写文件、联网或启动进程的方法。安装第三方插件前仍应读一遍：它能读取你有权读取的所有文件，并显示在 Knack 里。
- 代码同步执行，`list` 和 `read` 不能返回 Promise。
- 每个插件单线程执行，全局变量在多次调用间保留，可以用来做缓存；worker 被重启后清空，统计不能依赖它。
- 会话的 `version` 变化时整个会话重读，没有按字节增量读取。列表每次刷新都会调用，应保持轻量。
- 加载限时 5 秒，每次 `list` 或 `read` 限时 30 秒，单次结果上限 128 MiB。超时或超过内存上限时 worker 被结束，下次调用自动启动新的。`list` 或 `read` 连续超时或崩溃 3 次后插件停用，重启 Knack 后恢复。
- 内存上限：JavaScript 堆 1 GiB。SQLite 在这之外分配内存，单个值上限 64 MiB，整体没有硬性上限，只受调用时限约束。
- 一次刷新中，一个插件的 `read` 累计超过 10 秒后，其余会话留到下一次刷新，统计会分批出现。
- `read` 失败时保留上次的统计，并在警告中显示错误；同一 `version` 不再重试，数据变化或重启后才会再读。`list` 失败时沿用上次列出的会话。

## 简单标准记录的统计规则

- 有正文的 user 记录开始新的一轮；只带工具结果的记录不算新的一轮。
- 每条 assistant 记录是一次请求。请求耗时按上一条带时间的记录到这条记录的间隔估算，tok/s 由此估算。
- 工具调用耗时按调用记录到结果记录的间隔估算。
- 没有工具调用、带正文的 assistant 记录结束本轮；到下一条 user 记录之间算作等待你回复。
- 缺少用量字段的请求不按零计，会显示“暂不支持”。
- 以上简单字段不表达 Skill、实测思考耗时、权限等待和子 Agent 关联。需要这些信息时使用下面的扩展记录；内嵌 Claude/Codex 插件已使用扩展记录。

## 扩展记录协议 v1

`read` 返回的标准记录可以额外带 `stats` 和 `replay` 操作数组。提供某个数组时，该记录对应阶段使用该数组，代替简单 role 字段的默认处理；不提供则保持原规则。用户插件的记录仍用 `time` 和 `id`，按记录 ID 去重。操作按数组顺序执行，时间取记录的时间。它们是声明式数据，不能执行 Go 代码、写盘或联网。

`registerAgent` 可附带 `info`：`skills`、`outputRate`、`permissionWait`、`toolOutcome`，以及 `skillNote`、`usageNote`、`rateNote`、`timingNote`、`unsupported`。这些字段对应 `AgentInfo`，用于能力声明和缺失数据的说明；id/name 仍以顶层注册身份为准，不能通过 info 改写。未提供 info 时保留自定义 Agent 原有能力和文案。

### 统计操作 `stats`

| `op` | 字段与行为 |
| --- | --- |
| `known` | 将来源标记为可识别 |
| `metadata` | `id`、`cwd`、`branch`、`title` 更新非空元信息；`preserveChild` 保留子 Agent 的自身 ID；`replace` 允许 cwd/branch 置空；`identityOnce` 以第一笔非空 id 固定会话身份，其他 id 的重放元信息整笔忽略；`parent` 声明父会话 ID，同时用自身 ID 标识子 Agent |
| `begin` | `id` 开始一轮；`unique` 按非空轮次 ID 去重；`interruptNotice` 在已有轮次时改为中断该轮并设回复结束锚点。`command: true` 时从临时 `text` 识别 slash command 和纠正上一轮的提示，只保留推断结果 |
| `prompt` | 从本轮第一次 `text` 推断是否纠正上一轮，不创建新轮次；插件负责排除注入上下文 |
| `model` | `model` 设置后续请求的模型 |
| `input` | 将当前时间设为输入完成的锚点并清空累计思考；`resetPrompt` 重置本轮 prompt 标记 |
| `think` | 累加实测 `duration`（毫秒） |
| `request` | `id`、`model`、`tokens: {input, output, cacheRead, cacheWrite}`、`known`（用量是否完整）、`think`（实测毫秒）、`error`（API 错误）；同 ID 合并流式桶最大值与时间。`unique` 让重复请求整条忽略；`inputTiming` 用 input 锚点和累计 think 估算耗时，使用当前 model，消费后重置锚点。输入 Token 必须已扣除缓存。`correlateUsage` 将原生累计位置与 fallback 配对，撤销先到的 fallback，原生 inputTiming 不变 |
| `usageScope` | `scope` 声明接下来用量所属的历史来源 ID，与文件的固定会话身份分开；所有状态随检查点保存 |
| `fallbackUsage` | `key` 为规范化累计位置标识，`total` 为累计四桶（仅用于配对，不相加）、`tokens` 为本次四桶、`known` 表示用量完整。累计 key 去重；与原生累计位置匹配时不增加请求；反向上报按输入阶段/本次桶配对。未匹配的请求仅在快照中展现，模型与耗时采用 inputTiming，计时锚点独立于原生流。跨文件相同 scope/key 只保留一份，优先来源拥有者；父文件缺席时保留历史副本 |
| `use` | `id`、`name`、`input` 创建工具调用，自动识别 Skill 工具；`detectSkills` 从读取 SKILL.md 的 shell 命令识别 Skill 加载。input 只在本次处理时存在，缓存只留指纹和 Skill 元信息 |
| `result` | `id`、`output`、`error`；`result: {agentId, interrupted, timedOutAfterMs}`，`permission: {decision, source}`。关联工具和子 Agent、记录输出字节数、Skill 估算 Token、中断和超时。仅 `source: "user_temporary"` 计人工权限等待（整个工具间隔，估算） |
| `skillContent` | `id` 为关联工具 ID，`companion` 表示 slash command 的伴随注入；`text` 提供 Skill 目录和估算 Token，正文不保存 |
| `reply` | `force` 明确结束回复；否则 `content` 且无待完成工具时结束；`clear` 清除回复结束锚点 |
| `duration` | 当前轮日志报告的 `duration`（毫秒）。缺省表示未知 |
| `away` | 下次轮间等待归入 away，不额外伪造离开时长 |
| `event` | `kind` 和可选 `duration`（毫秒）；例如 compact、api_error、away_summary。API 错误请求后的下一请求由共享 reducer 标记为推断重试 |
| `interrupt` | 中断当前轮；`onlyExisting` 避免凭空创建轮次，`reply` 同时设回复结束锚点 |
| `finish` | `id` 匹配轮次（允许延迟完成前面的轮），`duration` 和 `interrupted` 记录报告耗时与中断；不提供 id 时取最近轮 |

子 Agent 必须有显式证据才关联到父轮：工具 result 的 agentId 与子日志的 AgentID 匹配。仅知道父会话 ID、不知道父轮时进入 `UnlinkedSubagents`，仍累计一次用量，不增加主会话数。

### 回放操作 `replay`

| `op` | 字段与行为 |
| --- | --- |
| `entry` | `kind: user/context/assistant/thinking/tool/event`；`text`、`name`、`rid`（请求 ID）、`error`。tool 用 `id` 和 `input`，按工具 ID 去重，结果到来前 End 未知；其余记录起止默认为当前时间。`once` 仅展示一次基础系统提示；`currentModel` 使用当前模型；`pending` 等待下个 associate 关联请求；`measured` 消费同名 timing 的实测起止。每个文本字段仍受 64 KiB 截断限制 |
| `result` | `id`、`text`、`error`，填入对应工具的输出和结束时间，重复结果忽略 |
| `model` | `model` 设置回放模型 |
| `associate` | `rid` 关联此前 pending 的条目并清空 pending |
| `timing` | `kind`、`from`、`to`（Unix 毫秒）存放下一条相应类型的实测时间；例如 Reasoning、AgentMessage |

统计检查点只保存归约后的元信息（含累计位置、配对计数、待确认请求的统计），不保存操作数组、text、input 或 output。回放操作仅在打开会话时使用，内容不进入统计缓存。

## 内嵌流式插件

源码在 `internal/session/plugins/{common,claude,codex}.js`，通过 `go:embed` 编入程序。它们以 `knack.registerAgent` 声明 `info`、`roots`、`suffixes`、`visit(parts, dir)`、`parent(path, parentID)`、可选 `readHeader(head)` 和 `readRecord(record, mode)`。`visit` 返回 `{ok, parent?, child?}`，parent 返回显式父日志路径；readHeader 从最多 4 KiB 前缀返回可恢复的纯元信息记录或 null，不返回正文；`mode` 为 stats 或 replay。readRecord 接收单条原始 JSON 对象，返回 `{timestamp, type, id, stats, replay}`；timestamp/type 用于保留原日志的时间锚点和有效记录检查。公共脚本在统计模式不输出回放操作，在回放模式不输出统计操作。源格式判断、Token 归一化、上下文分类等均在 JS；Go 只负责共享归约、文件边界和检查点。Factory 的 Go format 不变。

这是随程序审核的纯转换代码，在主进程独立 goja runtime 中同步运行，同一 runtime 加锁串行使用，所有会话状态归 Go 检查点所有。它没有 readText、walk、query、require、fetch 或启动进程的能力，也不能从日志生成并执行代码。用户目录的脚本不能选择这种执行方式，仍隔离在 worker。

选择这个边界是为了沿用 Reader 已有的流式保证：每次刷新 64 MiB、每文件 16 MiB、单行 4 MiB，超长 compacted 从前 4 KiB 恢复记录头，跨批次丢弃正文；未完成末行等待追加；按最近修改时间优先，支持前缀/尾部指纹、截断与替换重建。Codex 的 archived_sessions 和 `.jsonl.zst` 继续使用宿主有界解压；压缩文件的检查点为解压后偏移，恢复时重新解压并跳过已消费部分，巨大归档存在重复解压的 CPU 成本。

因此内嵌插件不走整个会话重读、30 秒调用、128 MiB IPC 结果或连续失败停用机制：再大的日志也可按字节预算逐批完成，而不是依靠放宽 worker 限制。代价是 JS 转换比原生 Go 多分配和运行开销，且随程序脚本的运行时缺陷没有子进程隔离；每次转换的输入受单行上限约束，取消在记录之间检查。这项信任仅适用于随程序发布的有限转换脚本。文件发现上限 20,000 和统计缓存上限 128 MiB 保持不变，达到边界仍明确显示不完整。

解析版本为 `stream/1/<SHA-256(common.js + agent.js)>`；共享 reducer 的语义变化必须增加 stream 协议版本，脚本变化自动改哈希。用户插件使用 `plugin/3/<脚本哈希>`。原 claude/3、codex/3 的统计在刷新时重建，其他 Agent 的缓存不受影响。

### Codex 旧日志的用量来源

`token_usage_record` 的 response_id 仍为首选。旧版 token_count 只使用 info.last_token_usage，并扣除 input 中的缓存桶；不累加 total_token_usage。累计字段的缺省零与显式零视为相同值。null info、缺失 last 或全零用量不产生零请求。两种格式同时出现时配对而非相加，先到的 fallback 不改变原生请求的 ID/模型/计时；跨批次和重启保存配对状态。

子日志会重放父 meta 甚至改写历史时间戳，因此第一笔 meta 固定身份/父关联，后续 meta 只更新历史来源。按来源与累计数值去除跨文件重复用量，仍保留子 Agent 自己新增的请求。没有父轮工具证据的子 Agent 继续进入 UnlinkedSubagents。没有 last 或原生 usage 的日志仍明确不支持用量，不据累计值推算历史。

## 排错

配置或插件有问题时，首页底部会提示“自定义 Agent 配置有 N 个问题”，悬停可以看到具体原因。有问题的 Agent 不会加载，其他 Agent 不受影响。

如果目录下有日志，但首页提示“没有可识别的会话记录”，通常是 `time` 或 `role` 的路径不对，或者 `roles` 没有包含日志里实际的角色值。
