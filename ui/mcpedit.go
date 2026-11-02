package ui

// mcpedit.go: 設定の画面の「MCP」から呼ぶメソッド(MCP のサーバーを足す・変える・消す・試しにつなぐ。0034)。
//
// 中身は aibow.MCPConfig にあり、ここは画面との中継だけ。画面から変えられるのは mcp.json の分だけで、settings.json の分は一覧に出すだけ。
// 保存したらすぐ効く(起動し直さない)。画面は保存のあとに、会話で選べる MCP のサーバー(MCPServers)を読み直す。
//
// デスクトップのアプリだけ(0034 の決定事項 2)。ブラウザ(aibow-server)の画面用の口には載せない(web.go の browserMethods に入れない)。
// 口の作りを間違えても、サーバーの PC で好きなプログラムを動かせないよう、ここでも断る(mcpConfig が nil)。

import (
	"context"
	"errors"

	"github.com/secondarykey/aibow"
	"github.com/secondarykey/aibow/mcp/client"
)

// errNoMCPEdit は、MCP のサーバーを画面から変えられないとき(ブラウザ・起動に失敗した)のエラー。
var errNoMCPEdit = errors.New("MCP のサーバーは、この画面からは変えられません(デスクトップのアプリの設定の画面の「MCP」で変えます)")

// mcpEditor は、画面から MCP のサーバーを変える係(使えなければエラー)。
func (s *Service) mcpEditor() (*aibow.MCPConfig, error) {
	if s.startupErr != nil {
		return nil, s.startupErr
	}
	if s.mcpConfig == nil || s.browser {
		return nil, errNoMCPEdit
	}
	return s.mcpConfig, nil
}

// MCPServerList は、設定の画面の「MCP」に出す一覧(settings.json と mcp.json のサーバー。env・headers の値は伏せる)。
func (s *Service) MCPServerList() (aibow.MCPServerList, error) {
	c, err := s.mcpEditor()
	if err != nil {
		return aibow.MCPServerList{}, err
	}
	return c.List(), nil
}

// AddMCPServers は、MCP のサーバーを mcp.json に足す(フォームの1つか、貼った JSON のいくつか)。画面のお知らせに出す文を返す。
// 足したサーバーは、新しい会話で最初から使わない(0034 の決定事項 12)。
func (s *Service) AddMCPServers(servers []aibow.MCPServerInput) ([]string, error) {
	c, err := s.mcpEditor()
	if err != nil {
		return nil, err
	}
	return c.Add(servers)
}

// UpdateMCPServer は、mcp.json のサーバー name の設定を変える(env・headers の空の値は前の値のまま)。動いていれば止める。
func (s *Service) UpdateMCPServer(name string, config client.ServerConfig) ([]string, error) {
	c, err := s.mcpEditor()
	if err != nil {
		return nil, err
	}
	return c.Update(name, config)
}

// DeleteMCPServer は、mcp.json のサーバー name を消す。動いていれば止める(tools.json の扱いは残す)。
func (s *Service) DeleteMCPServer(name string) ([]string, error) {
	c, err := s.mcpEditor()
	if err != nil {
		return nil, err
	}
	return c.Delete(name)
}

// TryMCPServer は、保存する前の設定で一時的に起動してツールの一覧を受け取り、止める(何も書かない)。
// add が false なら今あるサーバー name を変えるときで、env・headers の空の値は前の値で試す。
func (s *Service) TryMCPServer(ctx context.Context, name string, config client.ServerConfig, add bool) ([]aibow.KnownTool, error) {
	c, err := s.mcpEditor()
	if err != nil {
		return nil, err
	}
	return c.Try(ctx, name, config, add)
}

// ParseMCPJSON は、貼った JSON から足すサーバーを読み取る(足すのは AddMCPServers)。
func (s *Service) ParseMCPJSON(text string) ([]aibow.ParsedMCPServer, error) {
	c, err := s.mcpEditor()
	if err != nil {
		return nil, err
	}
	return c.ParseMCPJSON(text)
}
