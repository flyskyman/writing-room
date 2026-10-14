#!/usr/bin/env python3
"""打印工作台证据：上下文清单、最近回复、候选、裁定。用法: wb-inspect.py [最近回复条数]"""
import glob, json, sys
import os
L = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "run", "logs")
n = int(sys.argv[1]) if len(sys.argv) > 1 else 8
print("--- context.jsonl ---")
if os.path.exists(f"{L}/context.jsonl"):
    for l in open(f"{L}/context.jsonl"):
        m = json.loads(l)
        print(m["turn"], m["agent"], "model_sel=", m["model_sel"] or "default", "perm=", m["permission"], "resumed=", m["resumed"],
              "ms_v=", m["ms_version"], "dec=", m["decisions"], "delta=", m["delta_ids"], "read_upto=", m["read_upto"], "chars=", m["prompt_chars"])
print("--- replies (last %d) ---" % n)
sess = json.load(open(f"{L}/session.json"))
msgs = [json.loads(l) for l in open(f"{L}/{sess['log_file']}") if l.strip()]
for m in msgs[-n:]:
    print(m["id"], m["from"], m.get("model", ""), m["kind"], "read_upto=", m.get("read_upto"), "turn=", m.get("turn", ""), "dec=", m.get("decision", False), "|", m["text"][:200].replace("\n", " "))
print("--- manuscript ---")
d = json.load(open(f"{L}/manuscript.json"))
for v in d["versions"] or []:
    print("v%d" % v["id"], v["source"], v.get("note", ""), "|", v["text"][:140])
for c in d["candidates"] or []:
    print("cand", c["id"], c["agent"], c["model"], "base v%d" % c["base"], "dec", c["decisions"], "msg", c["msg_id"], c["status"], "|", c["text"][:140])
for x in d["decisions"] or []:
    print("decision", x["id"], "msg", x["msg_id"], "|", x["text"][:140])
