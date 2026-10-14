# 多模型医学写作工作台：任务与进度（2026-10-14）

关联 issue：flyskyman/ep-toolkit#679。调研快照：ep-toolkit `docs/research/writing-workbench-base_20261013.md`。
本文件是本轮开发循环的唯一进度记录，仓外（ep-toolkit-data/workbench-base/）。

## 任务（摘自作者 2026-10-14 /goal）

Web UI 内与 Claude、Codex、Gemini(agy) 围绕同一份材料交流：提问 → 多模型分析 → 作者插话/裁定 → 修改候选 → 接受/编辑 → 新版本继续。
基座：优先沿用已有开源实现；候选最多两个（AionUi、ai-agent-room）做关键路径实测。不 fork CCB，不另写群聊前端。

## 验收项

- A 三家真实模型同页回应「改善表达，保留 associated with 和 may，不增加新事实」，来源正确显示
- B 第二轮互评：实际输入中含另外两家发言且身份无误
- C 作者插话裁定「保留第一句，重写第二句，不增加限制段」后任务使用该裁定；旧任务结果不覆盖
- D 选候选 → 看差异 → 接受 → 手工微调 → 下一轮；实际输入正文为最新版本
- E 接受/拒绝/撤销/导出、刷新/重启恢复，恢复不重复派发
- F 超时/登录失效/单模型失败/重复事件/过期补丁：不当成功、无异议或自动共识（测试替身与真实分列）
- G 浏览器工具操作页面、查看控制台、截图

## 基座选型（2026-10-14）

| | AionUi v2.2.2 (Apache-2.0) | ai-agent-room 48f9281 (Apache-2.0) |
|---|---|---|
| 形态 | Electron 桌面应用；多 agent 各自会话；Team 模式 = Leader 拆任务派给 Teammate（MCP mailbox） | 本地 Web（127.0.0.1:8787），一个聊天室，人 + claude/codex/agy 同一时间线 |
| 三路接入 | ACP；Antigravity 在支持列表 | `claude -p --resume` / `codex exec resume` / `agy -p --conversation`（实测见下） |
| 同页发言 / 上下文转交 | 否：人只与 Leader 对话；Teammate 之间经 mailbox | 是：每家只收「自上次发言后的新消息」，带 `[#id from]` 来源标签与「读到 #N」 |
| 人工插话 / 停止 | 无同页插话 | 有：等待间隔内的人类消息转给下一位；Stop |
| 持久化 | 有 | logs/session.json + chat-*.jsonl，重启恢复，进行中的轮不重跑 |
| macOS 可运行 | 需装 Electron 应用（release 无 assets，未装） | 源码构建通过（项目内 Go 1.27.2），单测 ok |
| 结论 | 不满足「同页群聊」；未实跑（源码级核对） | **选用**。改动范围：加稿件/候选/决策状态与面板 |

## 进度记录

### 迭代 0（基线）
- 读取规则、调研快照、#679 评论；克隆 ai-agent-room、项目内下载 Go（go-dl/，非 brew）；`go build` / `go test ./...` 通过。
- 冒烟（真实，2 次顶层任务）：`@claude @agy` 自报模型 → claude-fable-5-1、Gemini 3.8 Flash (High) 各 7–13 s 回应。
- 作者裁定（2026-10-14 原话）：「codex read-only 不能写。真机实验可以接受」「在独立分支，worktree中操作，不要直接在main 上操作」→ 分支 `medical-workbench`，worktree `ai-agent-room-wb`，主检出干净。

### 迭代 1：稿件状态层 + 面板（commit 3df42c0 + 空切片修复）
- 新增 `src/manuscript.go`（版本/候选/裁定、提示词快照、`logs/context.jsonl` + `logs/prompts/` 输入记录、API）、`manuscript_test.go`；改 `chat.go`（Message.Turn/Decision、裁定取消进行中轮次、候选提取）、`main.go`（路由、lang zh）、`settings.go`、`web/index.html`（右栏面板、词级 diff、上下文对话框、「作为裁定」）。
- 启动：`wb-start.sh`（env -i 独立环境、只听 127.0.0.1:8787、`-max-hops 6 -delay 1`）；重建：`wb-rebuild.sh`；证据：`wb-inspect.py`。
- 设置：claude/codex 权限 read_only；agy 无权限开关（基座不支持），靠 wb-work/GEMINI.md 禁工具；agy 固定模型 gemini-3.8-flash-high；规则 rules.en.md = wb-rules.md（中文角色与写作规则）。
- **验收 A（真实，3 次）**：v1 导入后 `@all 改善表达…` → claude-fable-5-1 / gpt-6-sol / Gemini 3.8 Flash (High) 同页回应，各提取 1 个候选（基准 v1）；清单 turn-1..3：ms_v=1, delta=[9,10,11], perm read_only/read_only/default。截图 `run/logs/screens/A-round1-three-models.jpg`。
- 失败项：首次渲染 `candidates=null` 抛 TypeError（已修）。

### 迭代 2：验收 B / C（真实，各 3 次顶层任务；累计 14 次，含被取消的 3 次）
- **B**：`@all 第二轮：请各自评价另外两家的具体措辞…` → 清单 turn-4 agy delta=[12,13,15]、turn-5 claude delta=[13,14,15]、turn-6 codex delta=[12,14,15]（各自不含自己的发言）；输入文件 `prompts/turn-5-claude.txt` 第 2/8/20 行为 `[#13 codex（#11 まで読了）]`、`[#14 agy（…）]`、`[#15 human]`，无 role/assistant 字样；快照块含「稿件 v1」。页面「📋 上下文」对话框截图 `screens/B-round2-context-manifest.jpg`。
- **C**：先发 `@all 请各自再给一版候选…`（#19），3 s 后勾选「作为裁定」发 `保留第一句，重写第二句，不增加限制段。`（#22，裁定 1）。server.log：`manuscript.decision.cancel_running` → agy turn-8 `AGENT_CANCELED`（16.5 s 时中断）；claude/codex 已在裁定前回完（#20/#21 → 候选 4/5，快照 dec=0 → 面板标「未见裁定 1」、接受禁用，服务端 `MS_MISSED_DECISION`）。裁定后 turn-10/11/12 清单 dec=1；agy turn-11 delta=[16,17,19,20,21,22]（被取消轮的新消息连同裁定一起重发，cursor 未前进）。
- i18n：加 `I18N_ZH`（258 键，与 en 一一对应），`I18N_LANGS` 加 zh；服务端 lang 接受 zh（投递按 en）。待重建后切换界面验证。

### 迭代 3：验收 D / E / F（真实 1 次，累计 15；模拟 3 次替身调用）
- **D**：候选 #7（codex）「编辑后接受」改为 `…future research on Y.` → v2（source candidate:7、note 作者微调；系统消息「作者接受了 @codex 的候选 #7」）；其余待处理候选标「过期：基准 v1 ≠ 当前」。API 实测：接受候选 1 → 409 `MS_MISSED_DECISION`；base=9 接受候选 7 → 409 `MS_STALE`。随后 `@claude 请基于当前版本…引用版本号` → turn-13 清单 ms_version=2、decisions=1；输入文件第 27–28 行「稿件 v2：…research on Y.」；回复「收到的版本为稿件 v2」，候选 9 基准 v2。截图 `screens/D-accept-edited-v2.jpg`。
- **E**：拒绝候选 #6 → 已拒绝；撤销 → v3（undo:2，正文=v1），候选 9 变「过期：基准 v2 ≠ 当前」；导出：API 返回 `manuscript-v2.md`（含版本注释 + 正文），页面按钮触发浏览器下载（未在 ~/Downloads 找到文件，按浏览器下载设置而定，**未逐字核对下载件**）；刷新页面后稿件/候选/裁定/对话全部恢复；重启服务（wb-rebuild.sh）后 context.jsonl 行数不变、无 agent.turn.start → 不重复派发。截图 `screens/E-reject.jpg`、`E-undo-v3.png`、`E-refresh-restored.jpg`。
- **F（模拟，端口 8790，替身 wb-sim/fake-*，与真实分列）**：claude=fail（Login expired）→ 系统消息 `[AGENT_FAILED]` + 再试行按钮，无候选；agy=timeout（turn_timeout 60 s）→ `[AGENT_TIMEOUT]`；codex=dup → 同一回复两个相同候选块：旧版登记 2 个候选 → 已改为同回复去重（单测覆盖）。没有一家被当成共识，没有自动入稿。过期补丁：见 D 的 409。截图 `screens/F-sim-fail-timeout-dup.jpg`（该截图是旧二进制 + lang=zh 时的页面，面板空是旧版不认 zh 导致的重连循环，新版已修）。
- 真实联调用量：claude-fable-5-1 ×5、gpt-6-sol ×5、Gemini 3.8 Flash (High) ×5（其中 1 次被裁定取消）。费用：未知（订阅 CLI，无逐次计费数据）。

### 收尾（commit ae49f91）
- 重建重启后：lang=zh 界面中文（截图 `screens/G-zh-ui-after-restart.jpg`），控制台无本页错误（仅 Zotero 扩展报错）；`go test` ok；两棵树 `git status` 干净；只剩 8787 一个预览服务（模拟实例已停）。
- **状态：COMPLETE（A–G 均有证据）**。已知缺口：① agy 无基座权限开关，只读靠 GEMINI.md + 非交互模式（基座注记）；② 导出下载件未在 ~/Downloads 逐字核对（API 内容已核）；③ 候选只支持整段替换文本，不支持多段稿件分段候选；④ 提示词框架语（新着メッセージ 等）仍是基座日文，共享材料块为中文；⑤ 每次重启基座都重发「环境与规则」系统消息（基座行为）。
- 启动 / 停止：`wb-start.sh` / `pkill -f 'ai-agent-room -workdir'`；地址 `http://127.0.0.1:8787/?token=<run/logs/.token>`（token 只在本机）。

### 迭代 4：日文全部改中文（作者 2026-10-14 原话「这个把日文都改成中文」）
- 服务端：Go 源码 241 条日文字面量 + 20 条汉字词（系统消息、提示词框架、API 错误、CLI 启动帮助、默认规则、额度文案）批量换成中文（映射 `wb-ja-zh.json`，脚本 `wb-ja2zh.py`）；测试断言同步改为中文，`go test` 通过。中文界面时「环境与规则」按中文框架投递（规则文件仍用 rules.en.md）。
- 页面：i18n 已覆盖；补「请主持总结」文案、根元素 title 翻译。历史消息里旧的日文系统消息（重启前产生）保留原样。
- commit fc24eb5 → dd53c46 → 本轮收尾 commit；重建重启后新系统消息为中文（#34）。
