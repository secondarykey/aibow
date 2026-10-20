// aibow-server は、aibow の「頭」(llama.cpp で動かすモデルと書式)を OpenAI 互換の HTTP の API として出すプログラム(ステップ6。0012)。
// ステップ8(0015)で aibow-api から名前を変えた。モデルの一覧・切り替え・GPU の層数も持ち、画面のアプリもここにつなげる。
//
//	aibow-server [-model モデル] [-ctx 作業領域の大きさ] [-port ポート] [-config 設定ファイル]
//
// Cline などの既製のツールから http://127.0.0.1:8089/v1 につなぐ。待ち受けは 127.0.0.1 だけ(PC の外からはつなげない)。
//
// 中身は api パッケージにあり、ここは引数と環境変数を読み、エンジンとモデルを用意して起動するだけ
// (aibow-mcp と mcp/server の関係と同じ)。画面のアプリ(_cmd/aibow)とは別の exe にしているのは、
// VRAM 8GB にモデルを2つ載せられず、1つの作業領域を画面の会話と API で取り合うと KV キャッシュを壊し合うため。
// 画面のアプリが**アプリの中でモデルを動かしているときは、同時には使わない**(使うときはアプリを閉じる)。
// 画面のアプリをここにつないでいるとき(settings.json の serverUrl。0015)は、アプリは GPU を使わないので同時に動かせる。
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

func main() {
	f := defineFlags(flag.CommandLine)
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "使い方: %s [-model モデル] [-ctx 作業領域] [-port ポート] [-config 設定ファイル]\n\n", filepath.Base(os.Args[0]))
		fmt.Fprintln(flag.CommandLine.Output(), "aibow のモデルを OpenAI 互換の API(/v1/models・/v1/chat/completions)として出します。")
		fmt.Fprintln(flag.CommandLine.Output(), "強さの順は 引数 > 設定ファイル > 環境変数 > 既定 です。")
		flag.PrintDefaults()
	}
	flag.Parse()
	if *f.version {
		fmt.Println(logPrefix, appVersion())
		return
	}

	closer := startLog()
	defer closer.Close()

	if err := run(f); err != nil {
		log.Error(err.Error())
		closer.Close()
		os.Exit(1)
	}
}

func run(f *flags) error {
	if flag.NArg() > 0 {
		flag.Usage()
		return fmt.Errorf("知らない引数があります: %v", flag.Args())
	}
	var env envReader
	c, notices, err := resolve(flag.CommandLine, f, &env)
	if err != nil {
		return err
	}
	for _, n := range notices {
		log.Warn(n)
	}
	if c.File != "" {
		log.Noticef("設定ファイル: %s", absPath(c.File))
	}
	// 待ち受けるアドレスは、モデルを読み込む前に出しておく(読み込みに失敗しても、どこで待ち受けるつもりだったかが分かるように)
	log.Noticef("待ち受けるアドレス: http://127.0.0.1:%d(llama.cpp とモデルを読み込んでから待ち受けます)", c.Port)
	// llama.cpp・モデルの場所は設定(引数・設定ファイル・環境変数)、GPU の層数・返事の既定はアプリと同じ環境変数で決める
	// (画面の DB は読まない。DB はアプリのもの)
	nCtx, port := c.Ctx, c.Port
	cfg := engine.Config{
		LibPath:   c.Lib,
		NCtx:      nCtx,
		GPULayers: env.int("AIBOW_GPU_LAYERS", 999), // 999 = 全部 GPU に載せる
		LlamaLog:  os.Getenv("AIBOW_LLAMA_LOG") == "1",
	}
	catalog := models.Catalog{ModelsDir: c.ModelsDir, OllamaDir: models.OllamaDir()}
	defaults := api.Defaults{
		Temperature:     0.7,
		MaxTokens:       env.int("AIBOW_MAX_TOKENS", 8192),
		PresencePenalty: float32(env.float("AIBOW_PRESENCE_PENALTY", 1.5)),
		Think:           true,
	}
	if nCtx <= 0 || port <= 0 || defaults.MaxTokens <= 0 {
		env.errs = append(env.errs, fmt.Errorf("作業領域の大きさ(ctx)・ポート(port)・AIBOW_MAX_TOKENS は 1 以上にしてください: %d / %d / %d", nCtx, port, defaults.MaxTokens))
	}
	if _, err := os.Stat(cfg.LibPath); err != nil {
		env.errs = append(env.errs, fmt.Errorf("llama.cpp のフォルダが見つかりません: %s(相対パスはカレントフォルダが基準です。-lib・設定ファイルの lib・AIBOW_LIB で場所を指定してください)", absPath(cfg.LibPath)))
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

	state, err := loadState(c.StatePath)
	if err != nil {
		return err
	}
	mm := &modelManager{eng: eng, catalog: catalog, state: state, defaultLayers: cfg.GPULayers}
	m, err := firstModel(mm, c.Model)
	if err != nil {
		return err
	}
	// 起動のときのモデルも、そのモデルに覚えた GPU の層数で読み込む
	info, err := mm.Load(m)
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
		Handler:           api.New(eng, mm, nCtx, defaults).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		// 書き込みの時間の上限(WriteTimeout)は付けない。返事の生成は数十秒〜数分かかることがあるため
		BaseContext: func(net.Listener) context.Context { return ctx },
	}
	errc := make(chan error, 1)
	go func() { errc <- hs.Serve(ln) }()
	log.Noticef("%s %s を起動しました(返事の上限の既定 %d・presence penalty %.1f)",
		logPrefix, appVersion(), defaults.MaxTokens, defaults.PresencePenalty)
	// つなぐ側が書く値をそのまま出す(画面のアプリは /v1 なし、Cline などの OpenAI 互換のクライアントは /v1 付き)
	log.Noticef("画面のアプリのつなぎ先(settings.json の serverUrl / AIBOW_SERVER_URL): http://%s", addr)
	log.Noticef("OpenAI 互換のクライアント(Cline など)の Base URL: http://%s/v1", addr)

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

// firstModel は、起動のときに読み込むモデルを探す。ID・GGUF のパス・一覧の名前で探し、空なら一覧の先頭。
func firstModel(mm *modelManager, arg string) (models.Entry, error) {
	if arg != "" {
		return mm.Find(arg)
	}
	list := mm.List()
	if len(list) == 0 {
		return models.Entry{}, fmt.Errorf("モデルが1つも見つかりません。-models(AIBOW_MODELS_DIR)のフォルダに GGUF を置くか、ollama pull してください")
	}
	return list[0], nil
}
