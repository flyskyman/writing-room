#!/usr/bin/env python3
"""把 Go 源码副本里的日文字符串换成中文（映射 tools/ja-zh.json，长键优先，整文件替换）。
src/ 保持上游日文原样，只对 rebuild.sh 复制出的副本运行；我们新写的 manuscript*.go 本身是中文，不走映射。
用法: ja2zh.py --src <目录> [--tests]   替换（--tests 时只处理 *_test.go）
      ja2zh.py --src <目录> --check     列出非测试 .go 里仍含假名的字符串字面量，有则退出码 1"""
import glob, json, os, re, sys
B = os.path.dirname(os.path.abspath(__file__))
OWN = {"manuscript.go", "manuscript_test.go"}
KANA = re.compile(r"[぀-ヿ]")
# 注释、rune、解释型字符串、原始字符串；只取后两者
TOKEN = re.compile(r'//[^\n]*|/\*.*?\*/|\'(?:[^\'\\\n]|\\.)*\'|"((?:[^"\\\n]|\\.)*)"|`([^`]*)`', re.S)

# 不用 argparse：同目录的 inspect.py 会遮蔽标准库 inspect
args = sys.argv[1:]
if "--src" not in args or args.index("--src") + 1 >= len(args):
    sys.exit(__doc__)
src_dir = args[args.index("--src") + 1]
files = [f for f in sorted(glob.glob(os.path.join(src_dir, "*.go"))) if os.path.basename(f) not in OWN]
if not files:
    sys.exit(f"没有 .go 文件: {src_dir}")

if "--check" in args:
    left = 0
    for f in files:
        if f.endswith("_test.go"):
            continue
        src = open(f).read()
        for m in TOKEN.finditer(src):
            s = m.group(1) if m.group(1) is not None else m.group(2)
            if s and KANA.search(s):
                left += 1
                print(f"{os.path.basename(f)}:{src.count(chr(10), 0, m.start()) + 1}: {s[:80]}")
    print("kana literals:", left)
    sys.exit(1 if left else 0)

m = json.load(open(f"{B}/ja-zh.json"))
keys = sorted(m, key=len, reverse=True)
total = 0
for f in files:
    if f.endswith("_test.go") != ("--tests" in args):
        continue
    src = new = open(f).read()
    for k in keys:
        if k in new:
            total += new.count(k)
            new = new.replace(k, m[k])
    if new != src:
        open(f, "w").write(new)
        print("changed", os.path.basename(f))
print("replacements:", total)
