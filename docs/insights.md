# 诊断

侧栏「会话」下的「诊断」把会话统计和技能扫描放在一起看，回答“该改什么”。页面只读，跟随标题栏的时间范围和 Agent 筛选。唯一的写入是点「打开价格表」时，在没有价格表的情况下创建一份模板。

## 费用

按请求的模型和逐请求用量估算美元费用，请求按完成日期归属，子 Agent 的花费计入它所在的会话。

- 内置价格是 Anthropic 官方 API 的价格（美元 / 百万 Token）。缓存写入按 5 分钟缓存计，即输入价的 1.25 倍。Haiku 5.5 按 10 万 Token 以内的档位计。
- 订阅套餐、云平台和代理的实际计费不同，这里的数字只用于比较相对开销。
- 没有价格的模型不计入费用，单独列为“未定价 Token”，不会显示成 $0。`<synthetic>` 是 Claude Code 本地生成的占位回复，按 $0 计。
- 缓存节省 = 缓存读取 Token 按输入价计算时，比按缓存读取价多出的差额。

### 价格表

`~/.config/skill-manager/prices.json` 补充或覆盖价格，单位是美元 / 百万 Token：

```json
{
  "models": {
    "gpt-6.1-sol": {"input": 1.25, "output": 10, "cache_read": 0.125, "cache_write": 0},
    "claude-opus-5-5": {"input": 4, "output": 20, "cache_read": 0.2, "cache_write": 5}
  }
}
```

模型名不区分大小写。查找时先找完整名称，再找最长的前缀；带路径或前缀的名称（如 `custom:proxy/gpt-6.1-sol`）还会再用最后一段匹配一次。改完后点「重新读取」，或者重新扫描。

## 上下文

每个会话第一个有用量的请求的输入 Token（缓存读取 + 缓存写入 + 未缓存输入）。它包含系统提示词、工具定义、指令文件、技能列表和第一条消息，可以当作开会话的固定开销的上限。表格按 Agent 和工作目录统计中位数和 P90，峰值是会话中单个请求的最大输入。失败的 API 请求不计入。

## 技能

只统计 Agent 会直接加载的目录（Agent 目录和自带目录），共享池和仓库不算。

- **常驻**：技能名和描述在每个会话技能列表里占的 Token，按字符数估算，中日韩文字约 1 字 1 Token，其他约 4 字符 1 Token。
- **90 天未调用**：只对日志里有 Skill 调用记录的 Agent 计算，其他显示“暂不支持”。调用按技能名匹配，插件技能的 `插件:名称` 也算。
- **描述问题**：缺少 SKILL.md 或 description、front matter 无法解析、软链断开、描述超过 1024 字符或短于 40 字符、front matter 的 name 与目录名不同。没有写何时使用只作为建议。
- **描述相近**：同一 Agent 下两个描述的用词重合度（Jaccard，英文按 3 个字母以上的词，中文按相邻两字）达到 0.45。

同一个技能挂到多个 Agent 时只列一次，并注明有几个 Agent。

## 摩擦

- 失败 = 工具结果的错误标记、中断或超时。中断和超时目前只有 Claude Code 记录。
- **疑似循环**：同一轮里同一工具、同一输入连续调用 3 次以上。输入用规范化 JSON 的 SHA-256 前 8 字节比较，缓存不保存输入内容。`write_stdin`、`wait`、`BashOutput` 等轮询类工具重复调用是正常用法，不计入。旧缓存没有指纹，重新扫描后才能识别。
- **等待确认**：Claude Code 记录为“用户临时允许”的工具调用。`AskUserQuestion`、`ExitPlanMode` 等提问类工具等的是人回答问题，不计入。
- 常需确认的工具会对照「配置」页所选目录的 `permissions.allow`，没有规则的会单独提示。

## 项目

按会话的工作目录归类，汇总会话、轮次、Token、费用、工具失败率、开局中位数和常用技能。点一行查看该目录的会话，右键可以查看该目录生效的配置。

## 配置

读取在所选目录启动 Agent 时生效的配置：

| Agent | 文件 |
| --- | --- |
| Claude Code | `~/.claude/settings.json`，`~/.claude.json` 的 `mcpServers` 和该目录的本地 MCP，`~/.claude/CLAUDE.md`；项目里的 `.claude/settings.json`、`.claude/settings.local.json`、`.mcp.json`，以及从 home 到该目录每一层的 `CLAUDE.md`、`.claude/CLAUDE.md`、`CLAUDE.local.md` |
| Codex | `~/.codex/config.toml` 的 `[mcp_servers.*]`，`~/.codex/AGENTS.md`，从 home 到该目录每一层的 `AGENTS.md` |

CLAUDE.md 中代码以外的 `@路径` 导入会跟进，最多 5 层，导入文件的 Token 一并计入。MCP 只显示启动命令名或服务地址的主机名，hooks 只显示命令的第一个词。参数、环境变量和完整 URL 可能含密钥，都不显示。MCP 调用次数按日志里的 `mcp__<服务器>__<工具>` 统计，Codex 记录在 `namespace` 里的 MCP 工具也按这个格式归一，服务器名中的 `-` 和 `_` 视为相同。插件带来的 MCP 和技能目前不在此列。
