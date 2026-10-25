package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/secondarykey/aibow/auth"
)

// aibow-server の代わり:ログインの要る形で、me とトークンの口だけを持つ
func fakeAuthServer() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /aibow/auth/me", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"login":true,"name":""}`))
	})
	mux.HandleFunc("POST /aibow/auth/token", func(w http.ResponseWriter, r *http.Request) {
		var req map[string]string
		json.NewDecoder(r.Body).Decode(&req)
		if req["password"] != "pw" || req["device"] == "" {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":{"message":"名前かパスワードが違います","type":"invalid_request_error"}}`))
			return
		}
		json.NewEncoder(w).Encode(auth.AppLogin{Token: "aibowapp_x", Name: req["name"]})
	})
	return mux
}

// 自己署名の証明書:指紋を確かめる前は何も送らず、確かめた指紋なら通る。違う指紋なら断る
func TestProbeSelfSigned(t *testing.T) {
	srv := httptest.NewTLSServer(fakeAuthServer())
	defer srv.Close()
	fp := auth.Fingerprint(srv.Certificate().Raw)

	p, err := ProbeServer(context.Background(), srv.URL, "")
	if err != nil || !p.HTTPS || p.Trusted || p.SystemTrusted || p.Fingerprint != fp {
		t.Fatalf("確かめる前: %+v %v", p, err)
	}
	p, err = ProbeServer(context.Background(), srv.URL+"/v1/", fp)
	if err != nil || !p.Trusted || !p.Login || p.URL != srv.URL {
		t.Fatalf("指紋を確かめたあと: %+v %v", p, err)
	}

	c := New(srv.URL)
	c.PinCert(fp)
	got, err := c.AppLogin("bob", "pw", "pc1")
	if err != nil || got.Token != "aibowapp_x" || got.Name != "bob" {
		t.Errorf("ログイン: %+v %v", got, err)
	}
	if _, err := c.AppLogin("bob", "違う", "pc1"); err == nil {
		t.Error("違うパスワード")
	}

	other := New(srv.URL)
	other.PinCert("00:11")
	if _, err := other.AppLogin("bob", "pw", "pc1"); !errors.Is(err, ErrCertChanged) || errors.Is(err, ErrUnreachable) {
		t.Errorf("覚えた指紋と違う: %v", err)
	}
}

func TestProbeHTTP(t *testing.T) {
	srv := httptest.NewServer(fakeAuthServer())
	defer srv.Close()
	p, err := ProbeServer(context.Background(), srv.URL, "")
	if err != nil || p.HTTPS || !p.Trusted || !p.Login || p.Fingerprint != "" {
		t.Errorf("HTTP: %+v %v", p, err)
	}
	if _, err := ProbeServer(context.Background(), "ftp://x", ""); err == nil {
		t.Error("つなぎ先の形が違う")
	}
	notAibow := httptest.NewServer(http.NotFoundHandler())
	defer notAibow.Close()
	if _, err := ProbeServer(context.Background(), notAibow.URL, ""); err == nil {
		t.Error("aibow-server でないサーバー")
	}
}
