#!/bin/sh
# 模拟实例（测试替身，端口 8790）：三路 CLI 都是 tools/sim/fake-*，不调用真实模型。用于故障注入验收
R=$(cd "$(dirname "$0")/.." && pwd)
S="$R/tools/sim"
mkdir -p "$R/run/sim/logs" "$R/run/sim/config" "$R/run/sim/work"
cd "$R" || exit 1
exec env -i HOME="$HOME" USER="$USER" LANG=en_US.UTF-8 TMPDIR="${TMPDIR:-/tmp}" PATH=/usr/bin:/bin:/usr/sbin:/sbin:/opt/homebrew/bin:/Library/Frameworks/Python.framework/Versions/3.14/bin \
  WB_SIM_DIR="$R/run/sim" CLAUDE_BIN="$S/fake-claude" CODEX_BIN="$S/fake-codex" AGY_BIN="$S/fake-agy" \
  AI_AGENT_ROOM_LOG_DIR="$R/run/sim/logs" AI_AGENT_ROOM_CONFIG_DIR="$R/run/sim/config" \
  ./ai-agent-room -workdir "$R/run/sim/work" -no-open -port 8790 -delay 0 -max-hops 6 "$@"
