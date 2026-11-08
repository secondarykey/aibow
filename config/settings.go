package config

// settings.go: 設定ファイル(settings.json)。手で書く設定・その PC でどう起動するかの設定を置く(置き場所の分け方は doc.go)。
//
// 読むのは起動したときだけ。変えたらアプリを起動し直す。
//
// MCP のサーバー(mcpServers)も書けるが、起動したときに mcp.json へ移し、ここからは消す(置き場所を1つにして、画面から変える・消すため。
// 0034 の決定事項 13。移すのはルートの aibow の mcpfile.go の MoveFromSettings、消すのは RemoveSettingsKey)。

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"os"
	"slices"

	"github.com/secondarykey/aibow/mcp/client"
)

// SettingsFile は設定ファイルの中身。
type SettingsFile struct {
	// MCPServers は MCP のサーバー(名前 → 起動のしかた・つなぐ先)。形は多くのツールで使われている mcpServers に合わせている
	MCPServers map[string]client.ServerConfig `json:"mcpServers"`

	// ServerURL は、生成を頼む aibow-server のつなぎ先(例: http://127.0.0.1:8089)。空なら、アプリの中で llama.cpp を動かす(0015 の 1・20)。
	// 「その PC でどう起動するか」なので手で書くこのファイルに置く。環境変数 AIBOW_SERVER_URL より強い
	ServerURL string `json:"serverUrl"`
	// ServerKey は aibow-server の API キー(aibow-server user key <名前> で作ったもの。0017 の 10-2)。
	// 127.0.0.1 だけでログインの要らない aibow-server なら要らない。環境変数 AIBOW_SERVER_KEY より強い
	ServerKey string `json:"serverKey"`
	// ServerCert は、aibow-server の証明書のファイル(自己署名の証明書で HTTPS を待ち受けているとき、サーバーの PC から写したもの)。
	// 書けば、その証明書を信頼してつなぐ。環境変数 AIBOW_SERVER_CERT より強い
	ServerCert string `json:"serverCert"`
	// FetchAllowPrivate は、URL を読むツール(fetch_url)で、PC の中・社内のアドレス(127.0.0.1・192.168.x.x など)も読むか(0036 の決定事項 12)。
	// 既定は読まない。めったに使わず、危うさを分かって書く人だけが使うので、画面からは変えない
	FetchAllowPrivate bool `json:"fetchAllowPrivate"`
}

// LoadSettingsFile は設定ファイルを読む。ファイルが無ければ空の設定を返す(MCP を使わないだけで、エラーにしない)。
// 書き間違いは、何行目か・どのサーバーかが分かるエラーにする(手で書くファイルなので)。
func LoadSettingsFile(path string) (SettingsFile, error) {
	var s SettingsFile
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, fmt.Errorf("設定ファイル %s を読めません: %w", path, err)
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return SettingsFile{}, fmt.Errorf("設定ファイル %s を読めません(%s): %w", path, JSONErrorLine(data, err), err)
	}
	if err := CheckServerURL(s.ServerURL); err != nil {
		return SettingsFile{}, fmt.Errorf("設定ファイル %s の serverUrl: %w", path, err)
	}
	if err := CheckMCPServers(path, s.MCPServers); err != nil {
		return SettingsFile{}, err
	}
	return s, nil
}

// RemoveSettingsKey は、設定ファイル path から項目 key だけを消して書き直す(mcp.json へ移したあとの mcpServers を消すのに使う)。
//
// 手で書いたファイルなので、ほかの項目は並びも値もそのまま残す(aibow が知らない項目も落とさない)。
// SettingsFile に読んで書き戻すと、知らない項目が消え、並びも struct の順になるため、項目を1つずつ生のまま読んで並べ直す。
// 変わるのは字下げだけ(値の中も2つの空白で揃える)。JSON にはコメントが無いので、ほかに失うものは無い。
func RemoveSettingsKey(path, key string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("設定ファイル %s を読めません: %w", path, err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return fmt.Errorf("設定ファイル %s が {…} の形ではありません", path)
	}
	var buf bytes.Buffer
	buf.WriteString("{")
	n := 0
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return fmt.Errorf("設定ファイル %s を読めません: %w", path, err)
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return fmt.Errorf("設定ファイル %s を読めません: %w", path, err)
		}
		if t == key {
			continue
		}
		name, err := json.Marshal(t)
		if err != nil {
			return err
		}
		if n > 0 {
			buf.WriteString(",")
		}
		buf.WriteString("\n  ")
		buf.Write(name)
		buf.WriteString(": ")
		if err := json.Indent(&buf, v, "  ", "  "); err != nil {
			return err
		}
		n++
	}
	if n > 0 {
		buf.WriteString("\n")
	}
	buf.WriteString("}\n")

	// 書きかけのファイルが残らないよう、一時ファイルに書いてから名前を変える(tools.json と同じ)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("設定ファイル %s を書けません: %w", path, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("設定ファイル %s を書けません: %w", path, err)
	}
	return nil
}

// CheckMCPServers は、設定ファイル path に書いた mcpServers を確かめる(アプリの settings.json と aibow-server の server.json で同じ。0024)。
func CheckMCPServers(path string, servers map[string]client.ServerConfig) error {
	prefixes := map[string]string{} // ツールの名前の前に付ける形 → サーバーの名前
	for _, name := range slices.Sorted(maps.Keys(servers)) {
		if name == "" {
			return fmt.Errorf("設定ファイル %s の mcpServers に、名前が空のサーバーがあります", path)
		}
		// サーバーの名前はツールの名前の前に付く(filesystem__read_file)。英数字・_・- 以外は _ になるので、重なると区別できない
		p := client.ServerPrefix(name)
		if other, dup := prefixes[p]; dup {
			return fmt.Errorf("設定ファイル %s の mcpServers の %s と %s は、ツールの名前に付けると同じ %s__… になり、区別できません。"+
				"名前を変えてください(英数字・_・- だけで書くと、そのまま使われます)", path, other, name, p)
		}
		prefixes[p] = name
		if err := servers[name].Validate(); err != nil {
			return fmt.Errorf("設定ファイル %s の mcpServers の %s: %w", path, name, err)
		}
	}
	return nil
}

// JSONErrorLine は、JSON の読み間違いの場所を「N 行目」にする(手で書く・画面に貼る JSON の間違いを、場所の分かるエラーにするため。
// mcp.json・workspaces.json も使う)。
func JSONErrorLine(data []byte, err error) string {
	var off int64
	var syn *json.SyntaxError
	var typ *json.UnmarshalTypeError
	switch {
	case errors.As(err, &syn):
		off = syn.Offset
	case errors.As(err, &typ):
		off = typ.Offset
	default:
		return "JSON の形か欄の名前が違います"
	}
	return fmt.Sprintf("%d 行目のあたり", bytes.Count(data[:min(int(off), len(data))], []byte("\n"))+1)
}

// CheckServerURL は、aibow-server のつなぎ先が http(s)://ホスト[:ポート] の形かを確かめる(空はよい。使わないということ)。
// アプリが環境変数 AIBOW_SERVER_URL の値を確かめるのにも使う。
func CheckServerURL(raw string) error {
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("aibow-server のつなぎ先は http://127.0.0.1:8089 のように書いてください: %q", raw)
	}
	return nil
}
