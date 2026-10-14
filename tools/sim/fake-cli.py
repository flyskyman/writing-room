#!/usr/bin/env python3
"""测试替身（模拟，不是真实模型）：模仿 claude / codex / agy 非交互输出协议，用于验收 F 的故障注入。
用法：fake-cli.py <claude|codex|agy> [CLI 原参数…]
模式由文件 $WB_SIM_DIR/mode-<type> 控制：ok（默认）/ fail（登录失效类错误）/ timeout（不返回，等超时）/ dup（同一回复里重复两个候选块）
"""
import json, os, sys, time

kind = sys.argv[1]
args = sys.argv[2:]
sim = os.environ["WB_SIM_DIR"]  # 运行数据目录（tools/sim-start.sh 设置）
try:
    mode = open(os.path.join(sim, f"mode-{kind}")).read().strip() or "ok"
except FileNotFoundError:
    mode = "ok"
prompt = sys.stdin.read() if kind != "agy" else (args[args.index("-p") + 1] if "-p" in args else "")
with open(os.path.join(sim, f"calls-{kind}.log"), "a") as f:
    f.write(json.dumps({"t": time.strftime("%H:%M:%S"), "mode": mode, "args": [a for a in args if a != prompt][:12], "prompt_chars": len(prompt)}, ensure_ascii=False) + "\n")

if mode == "timeout":
    time.sleep(600)
    sys.exit(0)

ver = "v?"
for line in prompt.splitlines():
    if line.startswith("稿件 v"):
        ver = line[2:].rstrip("：:")
cand = f"```candidate\n[SIM {kind} {time.strftime('%H:%M:%S')}] In this retrospective study, X was associated with Y. Based on {ver}, this may inform future research.\n```"
text = f"【模拟回复·{kind}·{mode}】收到 {len(prompt)} 字输入。\n{cand}" + (f"\n再给一次（重复）：\n{cand}" if mode == "dup" else "")
fail_msg = "Login expired. Please run /login to authenticate (simulated)."

if kind == "claude":
    if mode == "fail":
        print(json.dumps({"type": "result", "subtype": "error", "is_error": True, "result": fail_msg, "session_id": "sim-claude-1"}))
    else:
        print(json.dumps({"type": "result", "subtype": "success", "is_error": False, "result": text, "session_id": "sim-claude-1",
                          "modelUsage": {"sim-claude": {"inputTokens": 10, "outputTokens": 5, "cacheReadInputTokens": 0, "cacheCreationInputTokens": 0}}}, ensure_ascii=False))
elif kind == "codex":
    print(json.dumps({"type": "thread.started", "thread_id": "sim-codex-1"}))
    if mode == "fail":
        print(json.dumps({"type": "turn.failed", "error": {"message": fail_msg}}))
        sys.exit(1)
    print(json.dumps({"type": "item.completed", "item": {"type": "agent_message", "text": text}}, ensure_ascii=False))
    print(json.dumps({"type": "turn.completed", "usage": {"input_tokens": 10, "cached_input_tokens": 0, "output_tokens": 5}}))
elif kind == "agy":
    if mode == "fail":
        print(json.dumps({"event": "result", "result": {"conversation_id": "sim-agy-1", "status": "ERROR", "error": fail_msg, "response": ""}}))
    else:
        print(json.dumps({"event": "result", "result": {"conversation_id": "sim-agy-1", "status": "SUCCESS", "response": text}}, ensure_ascii=False))
