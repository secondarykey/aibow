package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	apiclient "github.com/secondarykey/aibow/api/client"
)

// aibow-server に 401 で断られたのは「つながらない」ではない(ログインし直せば直る)。ServerError に入れず、LoginRequired で返す。
// つながらないときは ServerError に入れ、理由の種類を覚える(0057 の決定事項 5・6)
func TestModelsUnauthorizedIsNotServerDown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":{"message":"アプリのログインの期限(10 日)が切れました","type":"invalid_request_error","code":"token_expired"}}`))
	}))
	s := &Service{host: NewRemoteHost(apiclient.New(srv.URL))}

	r := s.Models(context.Background())
	if !r.LoginRequired || r.ServerError != "" || s.serverDown.Load() != nil {
		t.Errorf("401 はログインし直すだけ(つながらない、にしない): %+v", r)
	}

	srv.Close()
	r = s.Models(context.Background())
	if r.LoginRequired || r.ServerError == "" {
		t.Fatalf("止まっていれば、つながらない: %+v", r)
	}
	if down := s.serverDown.Load(); down == nil || *down != "unreachable" {
		t.Errorf("つながらない理由の種類を覚えるはず: %v", down)
	}
}
