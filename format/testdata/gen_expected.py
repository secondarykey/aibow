"""GGUF の中のチャットテンプレートを jinja2 で実際に通し、format パッケージのテストの期待値を作る(書式によらない1つのスクリプト。0026)。

    python format/testdata/gen_expected.py <書式の名前> <GGUF のパス か テンプレートのファイル(.jinja)> [出力の名前]

    例: python format/testdata/gen_expected.py qwen35 ~/.ollama/models/blobs/sha256-<qwen3.5 の GGUF>
        python format/testdata/gen_expected.py gemma4 _cmd/aibow/models/gemma-4-E4B-it-Q4_K_M.gguf
        python format/testdata/gen_expected.py llama3 ~/.ollama/models/blobs/sha256-<llama3.2:3b の GGUF>
        python format/testdata/gen_expected.py granite4 ~/.ollama/models/blobs/sha256-<granite4.1:3b の GGUF>
        python format/testdata/gen_expected.py granite4 <granite-4.0-micro の chat_template.jinja> granite40
        python format/testdata/gen_expected.py granite42 ~/.ollama/models/blobs/sha256-<granite4.2:3b の GGUF>

- 出力の名前を書くと、<書式の名前>/ ではなく <出力の名前>/ に書く。同じ書式で版の違うテンプレート(Granite 4.0 と 4.1。0029 の決定事項 9)の期待値を分けて持つため
- GGUF が手元に無い版は、Hugging Face のモデルの chat_template.jinja を渡す(種類と bos は確かめない。Granite 4.0 は
  https://huggingface.co/ibm-granite/granite-4.0-micro の chat_template.jinja)

- 書式の名前ごとに、GGUF の general.architecture と、要るならテンプレートの印を FORMATS に書く(aibow の format.ForModel と同じ決まり)。
  違う GGUF を渡したらエラーにする(llama の種類には Llama 3 の書式でないモデルもある。0027 の決定事項 2)
- テンプレートが今日の日付を使う(strftime_now。Llama 3.2)ときは、日付を TODAY に決め打ちする(期待値が日によって変わらないように。
  Go のテストも同じ日付を渡す)
- Llama 3 は1回の返事に呼び出し1つで、呼び出しのある返事の答えの文を書かない。aibow はそういう返事を分けて渡す(0027 の決定事項 7・8)ので、
  ここでも同じ形に分けてからテンプレートに渡す(split_for_llama3)。ユーザーの発言が無いままツールを渡すときは、テンプレートのもう一つの書き方
  (ツールをシステムに書く。tools_in_user_message=false)にする(0027 の決定事項 5。ケースの "tools_in_system")
- テンプレートは GGUF の tokenizer.chat_template から読む(Ollama のモデルなら ~/.ollama/models/blobs/ の中の、
  manifest で application/vnd.ollama.image.model になっているファイル)
- 会話は cases.json から読み、1件ごとに <書式の名前>/<name>.txt を書く。Go のテストは同じ cases.json を読んで比べる。
  同じ会話を書式ごとに並べて見比べられるよう、ケースは書式をまたいで共通にする。
  ある書式だけで試すケースは "formats": ["gemma4"] のように書く(書かなければ全部の書式)
- ツールは cases.json の "tools" に並べ、ケースの "tools" が true なら全部、名前の配列ならそのツールだけを知らせる
- 引数の値は aibow の tool.Arg の形(文字列の引数は中身、数・真偽・配列・オブジェクトの引数は JSON の文字。tool/arg.go)で書く。
  テンプレートには、ツールの定義の型を見て Python の値にして渡す(テンプレートは型のある値を書式ごとの書き方で書く)
- ツールの結果(role tool)は、直前の返事の呼び出しと順番で組にし、tool_call_id と name を付けて渡す(Gemma 4 は結果に名前を書くため)
- bos:aibow の書式は bos を文字で書かず、トークン化(llama.cpp の add_special)に任せる(0026 の決定事項 25)。
  テンプレートが先頭に bos を書くときは、期待値からその1つを外す(Gemma 4・Llama 3。Qwen3.5 は bos が無い。
  llama3.2 の GGUF は tokenizer.ggml.add_bos_token を書いていないが、llama.cpp は1つ足す。0027 の段1で確かめた)
- tojson は HuggingFace transformers と同じ形にする(区切りに空白・キーは書いた順・HTML のエスケープなし)。
  jinja2 に元からある tojson はキーを並べ替えて < > & をエスケープするので、モデルが学習で見た形と違う
- 必要なもの: Python 3 と jinja2 3.1 以上(items フィルタを使う)。一度作ったら、テンプレートかケースが変わるまで作り直さなくてよい
"""

import datetime
import json
import os
import struct
import sys

import jinja2
from jinja2.ext import loopcontrols
from jinja2.sandbox import ImmutableSandboxedEnvironment


def read_metadata(path, keys):
    """GGUF のヘッダーのメタデータから keys の値を読む(重みは読まない。語彙の配列は読み飛ばさずに読む)。"""
    types = {0: "B", 1: "b", 2: "H", 3: "h", 4: "I", 5: "i", 6: "f", 7: "?", 10: "Q", 11: "q", 12: "d"}

    def rd(f, fmt):
        return struct.unpack("<" + fmt, f.read(struct.calcsize("<" + fmt)))[0]

    def rs(f):
        return f.read(rd(f, "Q")).decode("utf-8")

    def value(f, t):
        if t == 8:
            return rs(f)
        if t == 9:
            et, n = rd(f, "I"), rd(f, "Q")
            return [value(f, et) for _ in range(n)]
        return rd(f, types[t])

    out = {}
    with open(path, "rb") as f:
        if f.read(4) != b"GGUF":
            sys.exit(f"GGUF ではありません: {path}")
        rd(f, "I")  # 版
        rd(f, "Q")  # テンソルの数
        for _ in range(rd(f, "Q")):
            key, t = rs(f), rd(f, "I")
            v = value(f, t)
            if key in keys:
                out[key] = v
    return out


def tojson(x, ensure_ascii=False, indent=None, separators=None, sort_keys=False):
    # transformers の chat_template_utils と同じ
    return json.dumps(x, ensure_ascii=ensure_ascii, indent=indent, separators=separators, sort_keys=sort_keys)


def raise_exception(message):
    raise jinja2.exceptions.TemplateError(message)


# tool/arg.go の typed と同じ:この型の引数は、値を JSON の文字で持つ
TYPED = {"integer", "number", "boolean", "array", "object"}


def arg_types(tool):
    """ツールの定義の引数ごとの型(type が配列なら null でない最初の型。tool.ArgTypes と同じ)。"""
    types = {}
    for name, p in tool["parameters"].get("properties", {}).items():
        t = p.get("type")
        if isinstance(t, list):
            t = next((x for x in t if x != "null"), None)
        if t:
            types[name] = t
    return types


def arguments(args, types):
    """tool.Arg の形の引数([名前, 値] の並び)を、テンプレートに渡す dict にする(書いた順のまま)。"""
    return {name: json.loads(value) if types.get(name) in TYPED else value for name, value in args}


def hf_messages(messages, tools):
    """cases.json の発言を、transformers の apply_chat_template に渡す形(OpenAI の形)にする(aibow の Message に合わせる)。"""
    types = {t["name"]: arg_types(t) for t in tools}
    out = []
    pending = []  # まだ結果の来ていない呼び出し(id, 名前)
    for i, m in enumerate(messages):
        hm = {"role": m["role"], "content": m["content"]}
        if m["role"] == "assistant":
            # aibow は思考を常に文字列で渡す(空でも)
            hm["reasoning_content"] = m.get("thinking", "")
            pending = []
            if m.get("tool_calls"):
                hm["tool_calls"] = []
                for j, c in enumerate(m["tool_calls"]):
                    call_id = f"call_{i}_{j}"
                    hm["tool_calls"].append({"id": call_id, "type": "function", "function": {
                        "name": c["name"], "arguments": arguments(c["args"], types.get(c["name"], {}))}})
                    pending.append((call_id, c["name"]))
        elif m["role"] == "tool" and pending:
            hm["tool_call_id"], hm["name"] = pending.pop(0)
        out.append(hm)
    return out


def split_for_llama3(messages):
    """Llama 3 に渡す形に分ける(0027 の決定事項 7・8。aibow の format/llama3.go と同じ決まり)。

    呼び出しのある返事は、答えの文があれば先に答えだけの返事にし、呼び出しは1つずつ「呼び出し → その結果」の組に並べ直す:
        assistant(答え・呼び出し A・B)、tool(A の結果)、tool(B の結果)
        → assistant(答え)、assistant(呼び出し A)、tool(A の結果)、assistant(呼び出し B)、tool(B の結果)
    """
    out = []
    i = 0
    while i < len(messages):
        m = messages[i]
        if m["role"] != "assistant" or not m.get("tool_calls"):
            out.append(m)
            i += 1
            continue
        results = []
        j = i + 1
        while j < len(messages) and messages[j]["role"] == "tool":
            results.append(messages[j])
            j += 1
        if m["content"].strip():
            out.append({"role": "assistant", "content": m["content"]})
        for k, call in enumerate(m["tool_calls"]):
            out.append({"role": "assistant", "content": "", "tool_calls": [call]})
            if k < len(results):
                out.append(results[k])
        out.extend(results[len(m["tool_calls"]):])
        i = j
    return out


# FORMATS は書式の名前 → (GGUF の種類, テンプレートの印)。aibow の format.ForModel の表と同じ(0027 の決定事項 2)。
# 印は、テンプレートにどれもあるときにその書式を選ぶ(Granite は 3.x・4.0/4.1・4.2 が同じ種類 granite で書式が違う。0029)
FORMATS = {
    "qwen35": ("qwen35", ()),
    "gemma4": ("gemma4", ()),
    "llama3": ("llama", ("<|start_header_id|>",)),
    "granite4": ("granite", ("<|start_of_role|>", "<tool_call>")),
    "granite42": ("granite", ("<|im_start|>", "<function=")),
}

# TODAY は、テンプレートが今日の日付を使うとき(strftime_now)の日付。Go のテストも同じ日付を渡す
TODAY = datetime.date(2026, 10, 30)


def main():
    for s in (sys.stdout, sys.stderr):
        s.reconfigure(encoding="utf-8")  # Windows でも日本語のメッセージが化けないように
    if len(sys.argv) not in (3, 4):
        sys.exit(__doc__)
    fmt, src = sys.argv[1], sys.argv[2]
    out_name = sys.argv[3] if len(sys.argv) == 4 else fmt
    if fmt not in FORMATS:
        sys.exit(f"知らない書式の名前です: {fmt}(使えるのは {', '.join(FORMATS)})")
    arch, marks = FORMATS[fmt]
    if src.endswith(".jinja"):
        # GGUF が手元に無い版のテンプレート(Hugging Face の chat_template.jinja)。種類と bos は確かめられない
        with open(src, encoding="utf-8") as f:
            meta = {"tokenizer.chat_template": f.read()}
    else:
        meta = read_metadata(src, {"general.architecture", "tokenizer.chat_template", "tokenizer.ggml.add_bos_token",
                                   "tokenizer.ggml.bos_token_id", "tokenizer.ggml.tokens"})
        if meta.get("general.architecture") != arch:
            sys.exit(f"GGUF の種類が {meta.get('general.architecture')} です({fmt} は {arch} の GGUF を渡してください)")
    if "tokenizer.chat_template" not in meta:
        sys.exit("tokenizer.chat_template が見つかりません")
    for mark in marks:
        if mark not in meta["tokenizer.chat_template"]:
            sys.exit(f"このテンプレートには {mark} がありません({fmt} の書式ではありません)")
    bos = ""
    if "tokenizer.ggml.bos_token_id" in meta:
        bos = meta["tokenizer.ggml.tokens"][meta["tokenizer.ggml.bos_token_id"]]
    add_bos = meta.get("tokenizer.ggml.add_bos_token", "(書いていない)")

    env = ImmutableSandboxedEnvironment(trim_blocks=True, lstrip_blocks=True, extensions=[loopcontrols])
    env.filters["tojson"] = tojson
    env.globals["raise_exception"] = raise_exception
    env.globals["strftime_now"] = lambda f: TODAY.strftime(f)
    template = env.from_string(meta["tokenizer.chat_template"])

    here = os.path.dirname(os.path.abspath(__file__))
    with open(os.path.join(here, "cases.json"), encoding="utf-8") as f:
        spec = json.load(f)
    out_dir = os.path.join(here, out_name)
    os.makedirs(out_dir, exist_ok=True)
    print(f"{fmt}: bos {bos!r}(add_bos_token={add_bos})")
    for c in spec["cases"]:
        if fmt not in c.get("formats", [fmt]):
            continue
        use = c.get("tools")
        tools = [t for t in spec["tools"] if use is True or (isinstance(use, list) and t["name"] in use)]
        messages = hf_messages(c["messages"], tools)
        extra = {}
        if fmt == "llama3":
            messages = split_for_llama3(messages)
            if c.get("tools_in_system"):
                extra["tools_in_user_message"] = False
        text = template.render(
            messages=messages,
            tools=[{"type": "function", "function": t} for t in tools] or None,
            add_generation_prompt=True,
            enable_thinking=c["think"],
            bos_token=bos,
            **extra,
        )
        if bos and text.startswith(bos):
            # bos はトークン化が足すので、書式は書かない。テンプレートが書いた1つを外す
            text = text[len(bos):]
        with open(os.path.join(out_dir, c["name"] + ".txt"), "w", encoding="utf-8", newline="") as f:
            f.write(text)
        print("wrote", out_name + "/" + c["name"] + ".txt")


if __name__ == "__main__":
    main()
