#!/bin/sh
# 重建并重启工作台。Go 工具链：优先 PATH 里的 go，否则用 GO_DIR（默认 ../ep-toolkit-data/workbench-base/go-dl/go）
R=$(cd "$(dirname "$0")/.." && pwd)
GO_DIR=${GO_DIR:-$R/../ep-toolkit-data/workbench-base/go-dl/go}
command -v go >/dev/null 2>&1 || export PATH="$GO_DIR/bin:$PATH"
export GOPATH="${GOPATH:-$R/run/go}" GOMODCACHE="${GOMODCACHE:-$R/run/go/mod}" GOCACHE="${GOCACHE:-$R/run/go/cache}"
cd "$R" || exit 1
go build -C src -o ../ai-agent-room . || exit 1
pkill -f 'ai-agent-room -workdir' ; sleep 1
(nohup "$R/tools/start.sh" > "$R/run/logs/stdout.txt" 2>&1 &)
sleep 2; curl -s -o /dev/null -w "http %{http_code}\n" http://127.0.0.1:8787/
grep -o 'token=[0-9a-f]*' "$R/run/logs/stdout.txt" | head -1 | cut -d= -f2 > "$R/run/logs/.token"
echo "URL: $(head -1 "$R/run/logs/stdout.txt" | sed 's/AI Agent Room: //')"
