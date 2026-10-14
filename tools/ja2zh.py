#!/usr/bin/env python3
"""把 src 里 Go 源码的日文字符串换成中文（映射 tools/ja-zh.json，长键优先，整文件替换）。
用法: wb-ja2zh.py [--tests]  （--tests 时只处理 *_test.go）"""
import glob, json, os, sys
B = os.path.dirname(os.path.abspath(__file__))
m = json.load(open(f"{B}/ja-zh.json"))
keys = sorted(m, key=len, reverse=True)
files = sorted(glob.glob(f"{B}/../src/*.go"))
tests = "--tests" in sys.argv
files = [f for f in files if f.endswith("_test.go") == tests]
total = 0
for f in files:
    src = new = open(f).read()
    for k in keys:
        if k in new:
            total += new.count(k)
            new = new.replace(k, m[k])
    if new != src:
        open(f, "w").write(new)
        print("changed", os.path.basename(f))
print("replacements:", total)
