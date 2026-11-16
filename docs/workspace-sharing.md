# 本地 Code 会话共享

`workspaces` 在 Claude Desktop 正常退出、profile 写锁已持有时，将本地 Code 会话索引合并到目标账号。它复用本机已有的 CLI transcript，不迁移云端 Chat、远端 Projects 或账号登录。

## 接口

```go
type Identity struct {
    AccountID      string
    OrganizationID string
}

type Source struct {
    Dir            string // 当前磁盘目录，可以是 parked profile。
    TranscriptRoot string // Code 的 projects 根目录，例如 ~/.claude/projects。
    Identity       Identity
    LiveDir        string // 激活后真实 userData 路径；缺省为 Dir。
}

func Sync(sources []Source, target Source, backupRoot string) (Report, error)
```

sources 应排除 target；包会忽略重复目录，并首先扫描 target 的记录和删除标记。LiveDir 用于避免将切换后失效的 parked 路径写入 scratch workspace 的 cwd。路径必须为绝对路径；已有组件中的符号链接会被拒绝。测试在临时目录中执行，不读取 HOME 或真实 Claude 数据。

Report 的 CodeCopied / CodeUpdated / Tombstoned 为成功提交的数量；失败时归零。Skipped 和 Reasons 只有固定理由和计数，没有标题、邮箱、路径或账号 ID。Reasons 还会记录非阻断说明 code_workspace_no_longer_exists / code_workspace_not_accessible：历史索引可见不等于原项目工作目录仍可用。

## 共享规则

- 扫描 `claude-code-sessions/<accountUuid>/<orgUuid>/local_<uuid>.json`。
- 以 cliSessionId 合并逻辑会话，较新 lastActivityAt 优先；同时间保留目标原记录。已存在的逻辑会话沿用目标 sessionId，避免来回切换重复导入。
- 目标 TranscriptRoot 必须存在对应 `<项目目录>/<cliSessionId>.jsonl`；未找到时明确跳过，不创建只有标题而没有对话内容的索引。
- 新索引只保留 sessionId、cliSessionId、cwd、originCwd、title、createdAt、lastActivityAt、lastFocusedAt、model、effort、isArchived，以及权限模式 permissionMode 和 Desktop 用来核对它的 autoChosenInApp、bypassChosenInApp，并标记 importedFrom=local-1p-code。不继承旧账号远端 MCP 配置、CCR/bridge ID、envScopeId、单个会话的权限放行规则（sessionPermissionUpdates、alwaysAllowedReasons）或认证字段。
- 权限模式取最近活动的那份记录；它没有时，取同一会话其他副本里最近活动且带模式的那份。缺少 autoChosenInApp / bypassChosenInApp 时，Desktop 会把自动和跳过权限确认降回手动，所以三个字段一起复制。
- Desktop 对 `importedFrom` 已设置且 `resumeConfirmed` 不为 true 的会话，打开时会询问「恢复已导入的会话？」。同一 cliSessionId 只要在任一账号里是原生会话（没有 importedFrom），或者已经确认过，导入的副本就写入 resumeConfirmed=true；只在别处导入、从未确认过的会话仍然询问。
- 目标里已经是最新的副本不覆盖，但会补两处：之前同步写入、可信却未确认的会话补上 resumeConfirmed；没有权限模式的会话从其他副本补上。记录里的其他字段保持原样。
- 每个 profile 的 `claude_desktop_config.json` 里，Desktop 按账号保存文件夹默认权限模式（`epitaxyPrefs.epitaxy-folder-permission-mode-v2.<账号>`）、已确认过的模式提示（`epitaxy-perm-mode-acks-v2.<账号>`）和跳过权限确认的开启状态（`bypassPermissionsOptInByAccount`）。同步时把其他账号的这些设置合并到目标账号名下：目标账号自己已有的文件夹设置和明确的「不开启」优先，其余取最近保存的配置；提示确认取并集。组织策略缓存 `bypassPermissionsGateByAccount` 不复制，由 Desktop 重新获取；文件里其他内容不改，有变化时才写入，旧文件先备份。
- 普通外部项目 cwd 原样保留。userData 内的 scratch-workspaces 同步用户文件，并映射到 LiveDir；其他内部工作目录（如需要单独处理的 git-shadow）明确跳过。外部目录不存在时报告说明，保留仍可阅读的历史。
- 不读取或合并其他会话系统、VM 磁盘、账号 memory 和调度任务。
- 不修改各账号目录里的 `archived-sessions.idx`。它只是 Desktop 用来推迟加载已归档会话的提示：列在里面的会话第二轮再读，没列的会话 Desktop 读前 4 KiB 看到顶层 `"isArchived": true` 也会推迟，两轮都会把全部会话读进来；文件读不了就不带提示照常加载。加载完成后 Desktop 按实际的已归档会话重写它，之后每次归档或取消归档也会更新。所以同步后它暂时过期，只影响加载顺序，不影响会话列表；正确性只取决于索引里的 `isArchived`，同步写入的记录很小，这个字段总在前 4 KiB 内。
- 会话里发布的 Artifact、评论和监听存在云端，属于发布时登录的账号。会话共享到另一个账号后，这些 Artifact 在新账号下查不到，评论监听也无法恢复；需要切回原账号，或在 claude.ai 上把 Artifact 共享给新账号。

scratch 文件复制保留权限和修改时间，目标同时间文件优先；拒绝符号链接、非普通文件，并排除凭据及执行配置：.credentials.json、Cookies、Local Storage、IndexedDB、.claude、.git、.mcp.json、CLAUDE.md / CLAUDE.local.md 等。用户工作文件中的普通 config.json 可以同步；Desktop 根部的账号 config.json 不在允许的目标路径里。

## 暂停与恢复

- 关闭会话共享只改 `state` 里的开关，不退出 Desktop、不动任何会话文件，同时记下当时使用的账号（`sharingPausedOn`）。
- 活动账号的任何变化（切换、添加、接管、恢复中断的切换）都会清除这条记录。
- 重新开启时先调用 `ResumeSessionSharing`：记录仍是当前账号，说明暂停期间没有别的账号运行过，其他账号的会话索引与上次合并时相同，合并不会有任何变化，于是只把开关打开，不重启 Desktop。
- 记录已清除或从未共享过时，`ResumeSessionSharing` 不做任何改动并返回 false，界面改走「立即同步」：确认后退出 Desktop、合并、再重新打开。

## 删除、备份与失败处理

`deleted_<uuid>`（兼容 `deleted_local_<uuid>`）是显式删除标记，优先于同 ID 索引并传播到目标；旧索引先备份再移出列表。源索引和公共 transcript 保留，没有索引本身不被推断为删除。

单个索引上限 10 MiB（与 Desktop 加载器一致），单文件 256 MiB，单次写入 2 GiB，索引最多 10,000 个、文件最多 50,000 个。新内容先在目标目录内的私有暂存目录准备。已有目标文件完整复制并同步到 `backupRoot/workspace-sync-*/<目标相对路径>` 后，才原子替换目标文件，避免先移走旧索引造成文件缺失窗口。

失败时反向恢复已处理文件，备份仍保留；回滚失败返回 ErrRollback，调用者必须持久阻断后续激活并要求人工恢复。进程在多文件提交之间崩溃时可能处于新旧文件混合状态，但已有索引保持旧或新完整版本，旧文件可按备份相对路径恢复。本包不提供自动 Recover API，也不宣称具有独立的全事务崩溃恢复状态机。

## 源码依据与验证边界

2026-11-09 只读分析本机 Claude.app 的 app.asar：

- .vite/build/index.chunk-CuH_hqMe.js：Code getStorageDir、registerExternalSession / finishRegisterExternalSession。
- .vite/build/index.chunk-qlkwmFgi.js：原生 Code 导入的最小持久化记录构造器 ic。

2026-11-17 只读分析 Claude Desktop 5.11492.5 的 app.asar：

- .vite/build/index.chunk-D1yJ0UIX.js：LocalSessionManager.loadSessionRecords 读取 archived-sessions hint、按前 4 KiB 判断归档并推迟加载，加载后由 archivedIdsHint.adopt 重写提示。同日本机一个账号目录有 47 个会话（40 个为导入），提示与记录里的已归档会话一致。

这些是 Desktop 内部磁盘协议，升级后需重新核对。模拟目录测试覆盖索引合并、去重、目标平局优先、删除传播、字段白名单、scratch 文件、符号链接拒绝、大小限制和写入失败回滚。真实 UI 可见性、打开历史及继续对话属于单独验收项。

