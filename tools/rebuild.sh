#!/bin/sh
# 重建并重启工作台。src/ 保持上游日文原样：复制到 run/build/src/，在副本上应用中文映射（tools/ja2zh.py），
# 原样与副本各跑一遍 vet/test，再用副本构建。--no-restart 时只构建不重启（CI 用）。
# Go 工具链：优先 PATH 里的 go，否则用 GO_DIR（默认 ../ep-toolkit-data/workbench-base/go-dl/go）
R=$(cd "$(dirname "$0")/.." && pwd)
GO_DIR=${GO_DIR:-$R/../ep-toolkit-data/workbench-base/go-dl/go}
command -v go >/dev/null 2>&1 || export PATH="$GO_DIR/bin:$PATH"
export GOPATH="${GOPATH:-$R/run/go}" GOMODCACHE="${GOMODCACHE:-$R/run/go/mod}" GOCACHE="${GOCACHE:-$R/run/go/cache}"
cd "$R" || exit 1
B="$R/run/build/src"
rm -rf "$B" && mkdir -p "$R/run/build" && cp -R src "$B" || exit 1
python3 tools/ja2zh.py --src "$B" && python3 tools/ja2zh.py --src "$B" --tests && python3 tools/ja2zh.py --src "$B" --check || exit 1
go vet -C src ./... && go test -C src ./... || exit 1
go vet -C "$B" ./... && go test -C "$B" ./... || exit 1
go build -C "$B" -o "$R/ai-agent-room" . || exit 1
[ "$1" = "--no-restart" ] && exit 0
pkill -f 'ai-agent-room -workdir' ; sleep 1
(nohup "$R/tools/start.sh" > "$R/run/logs/stdout.txt" 2>&1 &)
sleep 2; curl -s -o /dev/null -w "http %{http_code}\n" http://127.0.0.1:8787/
grep -o 'token=[0-9a-f]*' "$R/run/logs/stdout.txt" | head -1 | cut -d= -f2 > "$R/run/logs/.token"
echo "URL: $(head -1 "$R/run/logs/stdout.txt" | sed 's/AI Agent Room: //')"
