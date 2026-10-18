// aibow-api は、aibow の「頭」(llama.cpp で動かすモデルと書式)を OpenAI 互換の HTTP の API として出すプログラム(ステップ6。0012)。
//
//	aibow-api [-model モデル] [-ctx 作業領域の大きさ] [-port ポート]
//
// Cline などの既製のツールから http://127.0.0.1:8089/v1 につなぐ。待ち受けは 127.0.0.1 だけ(PC の外からはつなげない)。
//
// 中身は api パッケージにあり、ここは引数と環境変数を読み、エンジンとモデルを用意して起動するだけ
// (aibow-mcp と mcp/server の関係と同じ)。画面のアプリ(_cmd/aibow)とは別の exe にしているのは、
// VRAM 8GB にモデルを2つ載せられず、1つの作業領域を画面の会話と API で取り合うと KV キャッシュを壊し合うため。
// **画面のアプリと同時には使わない**(使うときはアプリを閉じる)。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/secondarykey/aibow/api"
	"github.com/secondarykey/aibow/engine"
	"github.com/secondarykey/aibow/log"
	"github.com/secondarykey/aibow/models"
)

// 起動の引数と環境変数の既定(0012 の未決事項をユーザーと決めた。2026-10-19)。
const (
	// defaultPort は待ち受けのポート。よく使われる番号(llama.cpp の server 8080・vLLM 8000・Ollama 11434 など)とぶつからないもの
	defaultPort = 8089
	// defaultCtx は作業領域の大きさ(プロンプト + 返事のトークン数)。アプリ(会話の上限 8192 + 返事の上限 8192)より大きいのは、
	// Cline などのシステムプロンプトが長いため。Qwen3.5 は KV キャッシュを持たない層(線形アテンション)が多く、8GB でも載る見込み
	defaultCtx = 32768
)

func main() {
	var env envReader
	modelArg := flag.String("model", os.Getenv("AIBOW_MODEL"), "読み込むモデル(ID・名前・GGUF のパス。例: qwen3.5:4b)。空なら一覧の先頭。環境変数 AIBOW_MODEL")
	nCtx := flag.Int("ctx", env.int("AIBOW_API_CTX", defaultCtx), "作業領域の大きさ(プロンプト + 返事のトークン数)。環境変数 AIBOW_API_CTX")
	port := flag.Int("port", env.int("AIBOW_API_PORT", defaultPort), "待ち受けのポート(127.0.0.1 だけで待ち受ける)。環境変数 AIBOW_API_PORT")
	showVersion := flag.Bool("version", false, "バージョンを表示して終わる")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "使い方: %s [-model モデル] [-ctx 作業領域] [-port ポート]\n\n", filepath.Base(os.Args[0]))
		fmt.Fprintln(flag.CommandLine.Output(), "aibow のモデルを OpenAI 互換の API(/v1/models・/v1/chat/completions)として出します。")
		fmt.Fprintln(flag.CommandLine.Output(), "引数を書かなければ環境変数、それも無ければ既定の値を使います。")
		flag.PrintDefaults()
	}
	flag.Parse()
	if *showVersion {
		fmt.Println(logPrefix, appVersion())
		return
	}

	closer := startLog()
	defer closer.Close()

	if err := run(&env, *modelArg, *nCtx, *port); err != nil {
		log.Error(err.Error())
		closer.Close()
		os.Exit(1)
	}
}

func run(env *envReader, modelArg string, nCtx, port int) error {
	if flag.NArg() > 0 {
		flag.Usage()
		return fmt.Errorf("知らない引数があります: %v", flag.Args())
	}
	// アプリと同じ環境変数で、llama.cpp・モデルの場所・GPU の層数・返事の既定を決める(画面の DB は読まない。DB はアプリのもの)
	cfg := engine.Config{
		LibPath:   env.str("AIBOW_LIB", "lib"),
		NCtx:      nCtx,
		GPULayers: env.int("AIBOW_GPU_LAYERS", 999), // 999 = 全部 GPU に載せる
		LlamaLog:  os.Getenv("AIBOW_LLAMA_LOG") == "1",
	}
	catalog := models.Catalog{ModelsDir: env.str("AIBOW_MODELS_DIR", "models"), OllamaDir: models.OllamaDir()}
	defaults := api.Defaults{
		Temperature:     0.7,
		MaxTokens:       env.int("AIBOW_MAX_TOKENS", 8192),
		PresencePenalty: float32(env.float("AIBOW_PRESENCE_PENALTY", 1.5)),
		Think:           true,
	}
	if nCtx <= 0 || defaults.MaxTokens <= 0 {
		env.errs = append(env.errs, fmt.Errorf("作業領域の大きさ(-ctx)と AIBOW_MAX_TOKENS は 1 以上にしてください: %d / %d", nCtx, defaults.MaxTokens))
	}
	if _, err := os.Stat(cfg.LibPath); err != nil {
		env.errs = append(env.errs, fmt.Errorf("llama.cpp のフォルダが見つかりません: %s(相対パスはカレントフォルダが基準です。AIBOW_LIB で場所を指定してください)", absPath(cfg.LibPath)))
	}
	if err := errors.Join(env.errs...); err != nil {
		return err
	}

	eng, err := engine.New(cfg)
	if err != nil {
		return err
	}
	defer eng.Close()
	log.Noticef("計算装置: %s", strings.Join(eng.Devices, " / "))
	if !eng.HasGPU {
		log.Warn("GPU が見つかりません。CPU で動かすので、生成はかなり遅くなります")
	}

	m, err := findModel(catalog, modelArg)
	if err != nil {
		return err
	}
	info, err := eng.LoadModel(m)
	if err != nil {
		return err
	}
	if info.Format == "" {
		return fmt.Errorf("モデル %s の書式(種類: %s)には未対応です。対応しているのは Qwen3.5 だけです", info.Name, info.Arch)
	}
	log.Noticef("モデルを読み込みました: %s(%s・書式 %s・作業領域 %d トークン・GPU %d 層 / %d 層・%.1f 秒)",
		info.ID, info.Desc, info.Format, info.NCtx, min(info.GPULayers, int(info.Layers)), info.Layers, info.LoadSec)

	// 127.0.0.1 だけで待ち受ける(PC の外からはつなげない。外から使うこと・API キーは 0012 の「やらないこと」)
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("%s で待ち受けられません。ほかのプログラムがこのポートを使っているなら、-port で変えてください: %w", addr, err)
	}

	// Ctrl+C で止める。リクエストの context をこれにつなぐので、生成中なら次のトークンで止まる
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	hs := &http.Server{
		Handler:           api.New(eng, defaults).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		// 書き込みの時間の上限(WriteTimeout)は付けない。返事の生成は数十秒〜数分かかることがあるため
		BaseContext: func(net.Listener) context.Context { return ctx },
	}
	errc := make(chan error, 1)
	go func() { errc <- hs.Serve(ln) }()
	log.Noticef("%s %s を起動しました。待ち受け: http://%s/v1(返事の上限の既定 %d・presence penalty %.1f)",
		logPrefix, appVersion(), addr, defaults.MaxTokens, defaults.PresencePenalty)

	select {
	case err := <-errc:
		return fmt.Errorf("HTTP の待ち受けが止まりました: %w", err)
	case <-ctx.Done():
	}
	log.Noticef("%s を終了します", logPrefix)
	sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return hs.Shutdown(sctx)
}

// findModel は、引数のモデルを一覧から探す。ID(ollama:qwen3.5:4b)・GGUF のパスのほか、
// 一覧の名前(qwen3.5:4b。ollama list と同じ表示)でもよい。空なら一覧の先頭。
func findModel(c models.Catalog, arg string) (models.Entry, error) {
	list := c.List()
	if arg == "" {
		if len(list) == 0 {
			return models.Entry{}, fmt.Errorf("モデルが1つも見つかりません。AIBOW_MODELS_DIR のフォルダに GGUF を置くか、ollama pull してください")
		}
		return list[0], nil
	}
	if m, err := c.Find(arg); err == nil {
		return m, nil
	}
	names := make([]string, 0, len(list))
	for _, m := range list {
		if m.Name == arg {
			return m, nil
		}
		names = append(names, m.Name)
	}
	return models.Entry{}, fmt.Errorf("モデル %q が見つかりません。使えるモデル: %s", arg, strings.Join(names, "・"))
}

// envReader は環境変数を読む。誤りはまとめて返す(形はアプリの _cmd/aibow/config.go と同じ)。
type envReader struct {
	errs []error
}

func (r *envReader) str(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func (r *envReader) int(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		r.errs = append(r.errs, fmt.Errorf("%s は整数で指定してください: %q", key, v))
		return def
	}
	return n
}

func (r *envReader) float(key string, def float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		r.errs = append(r.errs, fmt.Errorf("%s は数値で指定してください: %q", key, v))
		return def
	}
	return f
}

// absPath はログに出すためにパスを絶対パスにする(相対パスのままだと、どこを探したのかが分からないため)。
func absPath(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return p
}
