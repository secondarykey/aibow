"""Qwen3.5 の公式チャットテンプレートを jinja2 で実際に通し、format/qwen35.go のテストの期待値を作る。

    python format/testdata/qwen35/gen_expected.py <Qwen3.5 の GGUF のパス>

- テンプレートは GGUF の tokenizer.chat_template から読む(Ollama の qwen3.5:4b なら
  ~/.ollama/models/blobs/ の中の、manifest で application/vnd.ollama.image.model になっているファイル)
- 会話は cases.json から読み、1件ごとに <name>.txt を書く。Go のテストは同じ cases.json を読んで比べる
- tojson は HuggingFace transformers と同じ形にする(区切りに空白・キーは書いた順・HTML のエスケープなし)。
  jinja2 に元からある tojson はキーを並べ替えて < > & をエスケープするので、モデルが学習で見た形と違う
- 必要なもの: Python 3 と jinja2 3.1 以上(items フィルタを使う)。一度作ったら、テンプレートが変わるまで作り直さなくてよい
"""

import json
import os
import struct
import sys

import jinja2
from jinja2.ext import loopcontrols
from jinja2.sandbox import ImmutableSandboxedEnvironment


def read_chat_template(path):
    """GGUF のヘッダーのメタデータから tokenizer.chat_template を読む(重みは読まない)。"""
    types = {0: "B", 1: "b", 2: "H", 3: "h", 4: "I", 5: "i", 6: "f", 7: "?", 10: "Q", 11: "q", 12: "d"}

    def rd(f, fmt):
        return struct.unpack("<" + fmt, f.read(struct.calcsize("<" + fmt)))[0]

    def rs(f):
        return f.read(rd(f, "Q")).decode("utf-8")

    def skip(f, t):
        if t == 8:
            rs(f)
        elif t == 9:
            et, n = rd(f, "I"), rd(f, "Q")
            for _ in range(n):
                skip(f, et)
        else:
            f.seek(struct.calcsize("<" + types[t]), 1)

    with open(path, "rb") as f:
        if f.read(4) != b"GGUF":
            sys.exit(f"GGUF ではありません: {path}")
        rd(f, "I")  # 版
        rd(f, "Q")  # テンソルの数
        for _ in range(rd(f, "Q")):
            key, t = rs(f), rd(f, "I")
            if key == "tokenizer.chat_template" and t == 8:
                return rs(f)
            skip(f, t)
    sys.exit("tokenizer.chat_template が見つかりません")


def tojson(x, ensure_ascii=False, indent=None, separators=None, sort_keys=False):
    # transformers の chat_template_utils と同じ
    return json.dumps(x, ensure_ascii=ensure_ascii, indent=indent, separators=separators, sort_keys=sort_keys)


def raise_exception(message):
    raise jinja2.exceptions.TemplateError(message)


def hf_messages(messages):
    """cases.json の発言を、transformers の apply_chat_template に渡す形にする(aibow の Message に合わせる)。"""
    out = []
    for m in messages:
        hm = {"role": m["role"], "content": m["content"]}
        if m["role"] == "assistant":
            # aibow は思考を常に文字列で渡す(空でも)
            hm["reasoning_content"] = m.get("thinking", "")
            if m.get("tool_calls"):
                hm["tool_calls"] = [{"name": c["name"], "arguments": dict(c["args"])} for c in m["tool_calls"]]
        out.append(hm)
    return out


def main():
    if len(sys.argv) != 2:
        sys.exit(__doc__)
    env = ImmutableSandboxedEnvironment(trim_blocks=True, lstrip_blocks=True, extensions=[loopcontrols])
    env.filters["tojson"] = tojson
    env.globals["raise_exception"] = raise_exception
    template = env.from_string(read_chat_template(sys.argv[1]))

    here = os.path.dirname(os.path.abspath(__file__))
    with open(os.path.join(here, "cases.json"), encoding="utf-8") as f:
        spec = json.load(f)
    tools = [{"type": "function", "function": t} for t in spec["tools"]]
    for c in spec["cases"]:
        text = template.render(
            messages=hf_messages(c["messages"]),
            tools=tools if c.get("tools") else None,
            add_generation_prompt=True,
            enable_thinking=c["think"],
        )
        with open(os.path.join(here, c["name"] + ".txt"), "w", encoding="utf-8", newline="") as f:
            f.write(text)
        print("wrote", c["name"] + ".txt")


if __name__ == "__main__":
    main()
