# writing-room：多模型医学写作工作台

基于 [soft-jp-com/ai-agent-room](https://github.com/soft-jp-com/ai-agent-room)（Apache-2.0）的分支：作者与 Claude Code、Codex CLI、Antigravity(agy) 在同一页面围绕同一份稿件讨论，代理只返回修改候选，作者接受后由程序写入新版本。上游说明见 `README.md`；本文件只写本分支新增的部分。

## 新增能力

- 右栏稿件面板：版本历史、候选（词级差异）、接受 / 编辑后接受 / 拒绝 / 撤销、导出 Markdown。
- 每轮提示词冻结「当前稿件版本 + 作者裁定」；代理回复里的 ```candidate 块自动登记为候选（基准版本、所见裁定）。
- 「作为裁定」：进行中的轮次取消，之前的候选标「未见裁定」且不能直接接受；接受时重新校验基准版本（409）。
- 每次调用留下上下文清单与脱敏输入：`run/logs/context.jsonl`、`run/logs/prompts/`；页面「📋 上下文」可查。
- 界面与服务端文案为中文。`src/` 里的上游 Go 源码保持日文原样，中文在构建时生成：`tools/ja-zh.json`（日文→中文映射，唯一来源）由 `tools/ja2zh.py` 应用到 `run/build/src/` 副本，`--check` 列出副本里仍含假名的字符串字面量。

## 运行

```sh
tools/rebuild.sh      # 复制 src/ → 翻译副本 → vet/test（原样与副本两份）→ 构建 → 重启（Go 1.22+；没装 go 时用 GO_DIR 指向工具链）
tools/rebuild.sh --no-restart       # 只构建不重启（CI 用）
tools/start.sh        # 只启动；数据在 run/（gitignore）
pkill -f 'ai-agent-room -workdir'   # 停止
tools/sim-start.sh    # 测试替身实例（8790），故障注入用，不调真实模型
python3 tools/inspect.py [n]        # 打印清单、最近回复、候选、裁定
```

首次打开用 `run/logs/stdout.txt` 首行的带 token 地址。代理权限：claude / codex 设为只读；agy 无开关，靠 `run/work/GEMINI.md` 禁用工具。角色与写作规则见 `docs/rules.zh.md`，在「设置 → 规则」保存为 rules.en.md。

## 文档

- `docs/progress.md`：开发循环记录与验收证据（A–G）。
- `docs/rules.zh.md`：主笔 / 论证审阅 / 证据核验的角色与医学写作规则。

## 与 ep-toolkit 的关系

源于 ep-toolkit #679；代码层面不依赖 ep-toolkit。后续需求在本仓开 issue。
