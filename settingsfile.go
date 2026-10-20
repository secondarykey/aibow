package aibow

// settingsfile.go: 設定ファイル(settings.json)。手で書く設定・その PC でどう起動するかの設定を置く。
//
// 設定の置き場所は2つに分けている:
//
//	画面で変える設定 … DB(appsettings.go の app_settings と、会話ごとの conversations.settings)
//	手で書く設定     … このファイル。MCP のサーバー(mcpServers)と、生成を頼む aibow-server のつなぎ先(serverUrl。0015)
//
// MCP のサーバーの起動のしかた(npx のパスなど)は「その PC の設定」で、会話のデータとは性格が違う。
// 会話を別のサーバーに置く形(TODO.md のクライアント/サーバー構成の B)にしても、これは手元に残るもの。
// 読むのは起動したときだけ。変えたらアプリを起動し直す。

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
		return SettingsFile{}, fmt.Errorf("設定ファイル %s を読めません(%s): %w", path, jsonErrorLine(data, err), err)
	}
	if err := CheckServerURL(s.ServerURL); err != nil {
		return SettingsFile{}, fmt.Errorf("設定ファイル %s の serverUrl: %w", path, err)
	}
	prefixes := map[string]string{} // ツールの名前の前に付ける形 → サーバーの名前
	for _, name := range slices.Sorted(maps.Keys(s.MCPServers)) {
		if name == "" {
			return SettingsFile{}, fmt.Errorf("設定ファイル %s の mcpServers に、名前が空のサーバーがあります", path)
		}
		// サーバーの名前はツールの名前の前に付く(filesystem__read_file)。英数字・_・- 以外は _ になるので、重なると区別できない
		p := client.ServerPrefix(name)
		if other, dup := prefixes[p]; dup {
			return SettingsFile{}, fmt.Errorf("設定ファイル %s の mcpServers の %s と %s は、ツールの名前に付けると同じ %s__… になり、区別できません。"+
				"名前を変えてください(英数字・_・- だけで書くと、そのまま使われます)", path, other, name, p)
		}
		prefixes[p] = name
		if err := s.MCPServers[name].Validate(); err != nil {
			return SettingsFile{}, fmt.Errorf("設定ファイル %s の mcpServers の %s: %w", path, name, err)
		}
	}
	return s, nil
}

// jsonErrorLine は、JSON の読み間違いの場所を「N 行目」にする。
func jsonErrorLine(data []byte, err error) string {
	var off int64
	var syn *json.SyntaxError
	var typ *json.UnmarshalTypeError
	switch {
	case errors.As(err, &syn):
		off = syn.Offset
	case errors.As(err, &typ):
		off = typ.Offset
	default:
		return "JSON の形が違います"
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
