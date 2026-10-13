package aibow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSettings(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadSettingsFile(t *testing.T) {
	s, err := LoadSettingsFile(filepath.Join(t.TempDir(), "settings.json"))
	if err != nil || len(s.MCPServers) != 0 {
		t.Errorf("ファイルが無ければ空の設定: %+v, %v", s, err)
	}

	s, err = LoadSettingsFile(writeSettings(t, `{
  "mcpServers": {
    "filesystem": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "${workdir}"]
    }
  }
}`))
	fs := s.MCPServers["filesystem"]
	if err != nil || fs.Command != "npx" || len(fs.Args) != 3 || !fs.UsesWorkDir() {
		t.Errorf("%+v, %v", s, err)
	}

	// url のサーバー(Streamable HTTP)。headers に API キーなどを書ける
	s, err = LoadSettingsFile(writeSettings(t, `{"mcpServers": {"intra": {"type": "http", "url": "http://intra.example:3000/mcp", "headers": {"Authorization": "Bearer x"}}}}`))
	if intra := s.MCPServers["intra"]; err != nil || intra.URL != "http://intra.example:3000/mcp" || intra.Headers["Authorization"] != "Bearer x" {
		t.Errorf("%+v, %v", s, err)
	}

	for name, c := range map[string]struct{ body, want string }{
		"JSON の間違い":    {"{\n  \"mcpServers\": {\n    \"a\": {\"command\": \"x\",}\n  }\n}", "3 行目"},
		"型の間違い":        {"{\n  \"mcpServers\": {\n    \"a\": {\"command\": [\"x\"]}\n  }\n}", "3 行目"},
		"command が無い":  {`{"mcpServers": {"a": {"args": ["x"]}}}`, "mcpServers の a: command"},
		"古い SSE のサーバー": {`{"mcpServers": {"a": {"type": "sse", "url": "http://localhost/sse"}}}`, "SSE"},
	} {
		_, err := LoadSettingsFile(writeSettings(t, c.body))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v(期待: %q を含む)", name, err, c.want)
		}
	}
}
