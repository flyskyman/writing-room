#!/bin/sh
# 启动工作台（独立环境：不继承开发会话的环境变量；各 CLI 用自己的登录态）。
# 用法: tools/start.sh [ai-agent-room 其他参数]   数据在 run/（logs、work），只听 127.0.0.1:8787
R=$(cd "$(dirname "$0")/.." && pwd)
mkdir -p "$R/run/logs" "$R/run/work"
cd "$R" || exit 1
exec env -i HOME="$HOME" USER="$USER" LANG=en_US.UTF-8 TMPDIR="${TMPDIR:-/tmp}" \
  PATH="$HOME/.local/bin:$HOME/.gemini/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin" \
  AI_AGENT_ROOM_LOG_DIR="$R/run/logs" \
  ./ai-agent-room -workdir "$R/run/work" -no-open -port 8787 -delay 1 -max-hops 6 "$@"
