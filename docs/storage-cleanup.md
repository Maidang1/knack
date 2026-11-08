# 磁盘清理

从侧边栏「维护 → 磁盘清理」打开。进入页面后在后台扫描，点击「扫描磁盘」或 ⌘R 可刷新；不会在应用启动时额外遍历 Agent 存储。

## 文件清理

按最后修改时间保留最近 7、30、90 或 180 天，默认 30 天。列表展示每类内容的文件数、总大小和可处理大小。默认选择日志、缓存与 Shell 快照，历史内容需手动勾选。

| Agent | 内容 | 目录 | 默认选择 |
| --- | --- | --- | --- |
| Claude Code | 调试日志 | `~/.claude/debug` | 是 |
| Claude Code | Hook 调试日志 | `~/.claude/hook-debug-logs` | 是 |
| Claude Code | 缓存 | `~/.claude/cache` | 是 |
| Claude Code | Shell 快照 | `~/.claude/shell-snapshots` | 是 |
| Claude Code | 粘贴缓存 | `~/.claude/paste-cache` | 否 |
| Claude Code | 文件回退快照 | `~/.claude/file-history` | 否 |
| Claude Code | 会话日志 | `~/.claude/projects/**/*.jsonl` | 否 |
| Claude Code | 工具输出附件 | `~/.claude/projects/**/tool-results/**` | 否 |
| Claude Code | 历史计划 | `~/.claude/plans` | 否 |
| Codex | 运行日志 | `~/.codex/log` | 是 |
| Codex | 缓存 | `~/.codex/cache` | 是 |
| Codex | Shell 快照 | `~/.codex/shell_snapshots` | 是 |
| Codex | 会话日志 | `~/.codex/sessions/**/*.{jsonl,jsonl.zst}` | 否 |
| Codex | 已归档会话 | `~/.codex/archived_sessions/**/*.{jsonl,jsonl.zst}` | 否 |

Claude 的文件快照参与 `/rewind`，清理后旧会话的文件恢复能力会受影响。[Claude Code checkpointing 文档](https://code.claude.com/docs/en/checkpointing)

Claude 项目目录仅处理 `.jsonl`，保留 `memory` 等其他内容。Codex 会话文件清理后，数据库会话索引可能暂时保留。不会根据未验证的内部表结构删除会话索引。

清理前建议退出对应 Agent。确认后逐个重新验证文件身份、大小、修改时间和路径，流式生成 Zstandard 压缩备份，核对内容后移除原文件。发生变化或链接替换的文件会跳过并提示；每次最多成功处理 2000 个文件。扫描最多遍历 100000 个目录条目，最多检查 100 个数据库，达到限制或超时会提示结果不完整。

结果展示原文件大小、备份大小和两者的差额。这是文件逻辑大小的变化，不是操作系统分配块数或整盘剩余空间测量；压缩效果差的小文件可能增加占用。清理后可以点通知中的「撤销」，也可重启应用后在「清理备份」中恢复。原路径已有文件时拒绝覆盖，并保留备份。

备份存放在现有 `~/.config/skill-manager/undo/` 中独立的 `cleanup-file` 记录，目录权限为 `0700`，内容和记录权限为 `0600`（Windows 使用其文件系统权限机制）。点击「清空清理备份」并再次确认后永久移除这些备份，原有技能操作的撤销记录不受影响。

登录凭据、设置、技能、插件安装目录、生成图片、项目记忆和 `tmp/arg0` / `tmp/path` 可执行辅助目录不属于清理目录。扫描及备份处理均在本机进行。

## Claude Code 与 Codex SQLite 维护

页面按 Agent 分组显示数据库。Claude Code 的会话以 JSONL 保存，不能执行 SQLite 的碎片整理；2026-11-08 检查本机 Claude Code 2.1.309 的数据目录时没有发现 SQLite 数据库。文件格式与用途参见 [Claude Code 应用数据说明](https://code.claude.com/docs/en/claude-directory#application-data)。

Knack 同时提供基于实际文件的 SQLite 兼容探测：

- Claude Code：检查 `~/.claude` 及其 `state`、`sqlite`、`cache`、`daemon`、`sessions` 目录的直属数据库文件；不会进入第三方插件或项目目录寻找数据库。
- Codex：检查 `~/.codex` 和 `~/.codex/sqlite` 的直属数据库文件。

识别扩展名为 `.sqlite` / `.sqlite3` / `.db` 的文件（大小写不敏感），再验证 SQLite 文件头。发现有效数据库后提供维护操作；没有发现时明确显示状态，不创建数据库，也不将 JSON / JSONL 当作数据库处理。普通文件清理排除这些扩展名及 `-wal`、`-shm`、`-journal` 文件，避免将整库当作缓存删除。

显示数据库大小、`freelist_count × page_size` 的空闲页大小，以及 `-wal` 文件大小。空闲页是估算可回收量，不保证等于整理后的差额；WAL 包含已提交数据，不能直接删除。

- **整理碎片**：完整性检查、`wal_checkpoint(TRUNCATE)`、`VACUUM`、再次 checkpoint 和完整性检查。
- **合并 WAL**：执行相同检查与 checkpoint，跳过 `VACUUM`。

维护使用纯 Go SQLite 驱动，在三个桌面平台均无需额外安装 `sqlite3` 命令。使用 `mode=rw` 打开已有数据库，不创建缺失数据库，不删除表中记录，不手动移除 `-wal` / `-shm`。SQLite 自身的锁与事务负责一致性；占用、损坏、空间不足会报告错误。维护没有撤销备份。请先退出对应 Agent，`VACUUM` 可能需要约数据库大小两倍的临时空间。

参考：[SQLite VACUUM](https://www.sqlite.org/lang_vacuum.html)、[SQLite WAL checkpoint](https://www.sqlite.org/pragma.html#pragma_wal_checkpoint)。

## 自定义位置

当 Knack 使用真实用户 home 时，支持进程环境中的绝对路径 `CLAUDE_CONFIG_DIR`、`CODEX_HOME`、`CODEX_SQLITE_HOME`；测试 home 不继承这些环境变量。未读取 Codex 配置中的 `sqlite_home` / `log_dir`，因此仅通过配置文件重定向的位置需通过环境变量提供给 Knack。链接和 Windows junction 目录跳过并提示，不递归清理外部目标。

## 代码位置

- `internal/storage/scan.go`：类别白名单、占用扫描、保留天数与变化检测。
- `internal/storage/sqlite.go`：数据库识别、只读指标和安全打开。
- `internal/manage/cleanup.go`：压缩备份、文件清理、恢复及备份清空。
- `internal/manage/sqlite.go`：数据库维护。
- `internal/app/storage.go`：后台任务、页面、确认流程与结果展示。
