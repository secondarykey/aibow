package main

// config.go: aibow-server の設定(起動の引数・設定ファイル・環境変数)。
//
// 設定ファイルは2つに分ける(アプリの settings.json と tools.json と同じ分け方。0015 の決定事項 19):
//
//	server.json         … 手で書く設定(ポート・作業領域・最初のモデル・llama.cpp とモデルの場所)。aibow-server は書かない
//	server-models.json  … aibow-server が書く値(モデルごとの GPU の層数など)。手で書かなくてよい
//
// 置き場所はアプリと同じ %APPDATA%\aibow\(-config で手で書くほうのファイルを指せば、書くほうのファイルもその隣に置く)。
// アプリの settings.json と分けているのは、サーバーの PC とアプリの PC が別になりうるため(ステップ10)。
//
// 同じ設定を何か所にも書けるので、強さの順を決めている:引数 > 設定ファイル > 環境変数 > 既定。
// 「環境変数は初期値で、決めて保存した値が優先」というアプリの考え方と同じで、ファイルに書いた値は環境変数より強い。
// 引数はそのときだけ試すためのものなので、いちばん強い。

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// 設定ファイルの名前(アプリの settings.json・tools.json・aibow.db と重ならない名前)
const (
	configFileName = "server.json"
	stateFileName  = "server-models.json"
)

// 起動の引数と環境変数の既定(0012 の未決事項をユーザーと決めた。2026-10-19)。
const (
	// defaultPort は待ち受けのポート。よく使われる番号(llama.cpp の server 8080・vLLM 8000・Ollama 11434 など)とぶつからないもの
	defaultPort = 8089
	// defaultCtx は作業領域の大きさ(プロンプト + 返事のトークン数)。アプリ(会話の上限 8192 + 返事の上限 8192)より大きいのは、
	// Cline などのシステムプロンプトが長いため。Qwen3.5 は KV キャッシュを持たない層(線形アテンション)が多く、8GB でも載る見込み
	defaultCtx = 32768
)

// configFile は server.json の中身。書いていない欄は空(0)で、そのときは環境変数か既定を使う。
type configFile struct {
	Port      int    `json:"port"`      // 待ち受けのポート
	Ctx       int    `json:"ctx"`       // 作業領域の大きさ
	Model     string `json:"model"`     // 起動のときに読み込むモデル(ID・名前・GGUF のパス)
	Lib       string `json:"lib"`       // llama.cpp の DLL のフォルダ
	ModelsDir string `json:"modelsDir"` // GGUF を置くフォルダ(Ollama の保存先は別に探す)
	LogLevel  string `json:"logLevel"`  // ログのレベル(debug / info / notice / warn / error)。-debug があればそちら(log.go の logLevel)
}

// config は、強さの順で決めた設定。
type config struct {
	Port      int
	Ctx       int
	Model     string
	Lib       string
	ModelsDir string
	LogLevel  string // 設定ファイルの logLevel(空なら -debug か既定)

	// File は読んだ設定ファイル(無ければ空)。StatePath は aibow-server が書くファイル(server-models.json)
	File      string
	StatePath string
}

// flags は起動の引数。書かれたかどうかは set で見る(書かれていない引数は、設定ファイル・環境変数より弱いため)。
type flags struct {
	config, model, lib, modelsDir *string
	ctx, port                     *int
	version, debug                *bool
	set                           map[string]bool
}

// defineFlags は引数を決める。既定の値は空にしておき、どこから値を取るかは resolve で決める。
func defineFlags(fs *flag.FlagSet) *flags {
	return &flags{
		config:    fs.String("config", "", "手で書く設定ファイル(既定 %APPDATA%\\aibow\\"+configFileName+")。aibow-server が書く "+stateFileName+" はその隣に置く"),
		model:     fs.String("model", "", "読み込むモデル(ID・名前・GGUF のパス。例: qwen3.5:4b)。空なら一覧の先頭から、書式に対応している最初のモデル。設定ファイルの model・環境変数 AIBOW_MODEL"),
		ctx:       fs.Int("ctx", 0, fmt.Sprintf("作業領域の大きさ(プロンプト + 返事のトークン数。既定 %d)。設定ファイルの ctx・環境変数 AIBOW_SERVER_CTX", defaultCtx)),
		port:      fs.Int("port", 0, fmt.Sprintf("待ち受けのポート(127.0.0.1 だけで待ち受ける。既定 %d)。設定ファイルの port・環境変数 AIBOW_SERVER_PORT", defaultPort)),
		lib:       fs.String("lib", "", "llama.cpp の DLL のフォルダ(既定 lib)。設定ファイルの lib・環境変数 AIBOW_LIB"),
		modelsDir: fs.String("models", "", "GGUF を置くフォルダ(既定 models)。設定ファイルの modelsDir・環境変数 AIBOW_MODELS_DIR"),
		version:   fs.Bool("version", false, "バージョンを表示して終わる"),
		debug:     fs.Bool("debug", false, "ログを Debug レベルまで出す(設定ファイルの logLevel より強い。どちらも無ければ go run は Info、exe は Notice)"),
	}
}

// resolve は、引数 > 設定ファイル > 環境変数 > 既定 の順で設定を決める。
// 使われなくなった環境変数は notices に入れる(起動のときに Warn で書く)。
func resolve(fs *flag.FlagSet, f *flags, env *envReader) (config, []string, error) {
	f.set = map[string]bool{}
	fs.Visit(func(fl *flag.Flag) { f.set[fl.Name] = true })

	path, explicit := *f.config, f.set["config"]
	if !explicit {
		dir, err := os.UserConfigDir()
		if err != nil {
			return config{}, nil, fmt.Errorf("設定ファイルの置き場所(%%APPDATA%%)が決められません。-config で設定ファイルを指定してください: %w", err)
		}
		path = filepath.Join(dir, "aibow", configFileName)
	}
	file, found, err := loadConfigFile(path)
	if err != nil {
		return config{}, nil, err
	}
	if explicit && !found {
		return config{}, nil, fmt.Errorf("-config の設定ファイルが見つかりません: %s", absPath(path))
	}

	c := config{StatePath: filepath.Join(filepath.Dir(path), stateFileName)}
	if found {
		c.File = path
	}
	c.Port = pick(f.set["port"], *f.port, file.Port, env.int("AIBOW_SERVER_PORT", 0), defaultPort)
	c.Ctx = pick(f.set["ctx"], *f.ctx, file.Ctx, env.int("AIBOW_SERVER_CTX", 0), defaultCtx)
	c.Model = pick(f.set["model"], *f.model, file.Model, os.Getenv("AIBOW_MODEL"), "")
	c.Lib = pick(f.set["lib"], *f.lib, file.Lib, os.Getenv("AIBOW_LIB"), "lib")
	c.ModelsDir = pick(f.set["models"], *f.modelsDir, file.ModelsDir, os.Getenv("AIBOW_MODELS_DIR"), "models")
	c.LogLevel = file.LogLevel

	// aibow-api から名前を変えたとき(0015 の 18)に、環境変数の名前も変えた。古い名前は読まずに注意だけ出す
	// (黙って無視すると、書いたつもりの値が効かない理由が分からないため。アプリが AIBOW_CTX をやめたときと同じやり方)
	var notices []string
	for old, now := range map[string]string{"AIBOW_API_PORT": "AIBOW_SERVER_PORT", "AIBOW_API_CTX": "AIBOW_SERVER_CTX"} {
		if v := os.Getenv(old); v != "" {
			notices = append(notices, fmt.Sprintf("環境変数 %s(%s)は使われなくなりました。%s に名前を変えてください(aibow-api を aibow-server に名前を変えたため)", old, v, now))
		}
	}
	return c, notices, nil
}

// pick は、引数(書かれていれば)> 設定ファイル > 環境変数 > 既定 の順で、最初に決まっている値を返す。空(0)は「決めていない」。
func pick[T comparable](argSet bool, arg, file, env, def T) T {
	var zero T
	switch {
	case argSet:
		return arg
	case file != zero:
		return file
	case env != zero:
		return env
	}
	return def
}

// loadConfigFile は手で書く設定ファイルを読む。無ければ found が false(エラーにしない)。
// 知らない欄はエラーにする(書き間違えた欄が黙って効かないと、理由が分からないため)。
func loadConfigFile(path string) (file configFile, found bool, err error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return configFile{}, false, nil
	}
	if err != nil {
		return configFile{}, false, fmt.Errorf("設定ファイル %s を読めません: %w", path, err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&file); err != nil {
		return configFile{}, false, fmt.Errorf("設定ファイル %s を読めません(%s): %w", path, jsonErrorLine(data, err), err)
	}
	if _, ok := logLevels[file.LogLevel]; file.LogLevel != "" && !ok {
		return configFile{}, false, fmt.Errorf("設定ファイル %s の logLevel は debug・info・notice・warn・error のどれかにしてください: %q", path, file.LogLevel)
	}
	if file.Port < 0 || file.Ctx < 0 {
		return configFile{}, false, fmt.Errorf("設定ファイル %s の port と ctx は 1 以上にしてください: %d / %d", path, file.Port, file.Ctx)
	}
	return file, true, nil
}

// jsonErrorLine は、JSON の読み間違いの場所を「N 行目」にする(手で書くファイルなので。形はアプリの settings.json と同じ)。
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
		return "JSON の形か欄の名前が違います"
	}
	return fmt.Sprintf("%d 行目のあたり", bytes.Count(data[:min(int(off), len(data))], []byte("\n"))+1)
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
