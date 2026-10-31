package engine

// vision.go: 画像を読む部品(mmproj。画像エンコーダー)と、画像の付いたプロンプトをチャンクの列にする部分(0030)。
//
// 画像を読めるモデル(Qwen3.5 など)は、本体とは別に mmproj の GGUF を持つ(Ollama の目録の projector の層)。
// mmproj は画像を「画像トークンの埋め込み」にする小さなモデル(Qwen3.5 4B 用は 3 億パラメータ・BF16 で 0.7 GB)。
// llama.cpp の mtmd(マルチモーダル)の関数で読み、yzma の pkg/mtmd から呼ぶ(cgo なし。llama.cpp と同じ DLL の束の mtmd.dll)。
//
// mmproj は最初に画像を送ったとき(数えるときも)に読み込む(画像を使わない人の VRAM を減らさない。決定事項 13)。
// GPU に載せ、載らなければ CPU で読み直す(決定事項 23。CPU だとエンコードが 15〜30 倍遅い)。モデルを降ろす・替えるときに一緒に解放する。

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/hybridgroup/yzma/pkg/llama"
	"github.com/hybridgroup/yzma/pkg/mtmd"

	"github.com/secondarykey/aibow/format"
)

// DefaultImageMaxTokens は、画像1枚のトークン数の上限の既定(決定事項 22)。
// 画像は 32 ピクセル四方で 1 トークンなので、そのままだと 1 枚で 4,000 トークンほど(mmproj の上限)まで使う。
// 会話の上限の既定(8,192)の 1/8 に抑える(およそ 1024×1024 の細かさ。大きい画像は mtmd が縮める)
const DefaultImageMaxTokens = 1024

// ErrNoVision は、画像を読む部品(mmproj)の無いモデルに画像を渡したときのエラー(黙って画像を捨てない。決定事項 12)。
// 読めるモデルの名前は書かない(対応するモデルは増えていくので、名前を挙げるとすぐ古くなる。2026-10-31、ユーザーの指示)
var ErrNoVision = errors.New("今のモデルは画像に対応していません(画像を読む部品 mmproj がありません)。画像に対応したモデルに切り替えてください")

// VisionError は、画像を読めない理由のエラー(errors.Is(err, ErrNoVision) になる)。
// 画像を読めない書式・aibow-server 越しなど、理由ごとに文を変えつつ、使う側(メーター)が「画像を読めないから数えられない」と見分けられるようにする。
func VisionError(msg string) error { return visionError(msg) }

type visionError string

func (e visionError) Error() string        { return string(e) }
func (e visionError) Is(target error) bool { return target == ErrNoVision }

// projector は、読み込んだ mmproj。mu は読み込み・エンコード・トークン化をまとめて守る
// (mtmd の関数はスレッドセーフではない。トークン数を数える CountTokens は生成と同時に呼ばれることがある)。
type projector struct {
	mu    sync.Mutex
	path  string       // mmproj の GGUF。空なら画像を読めないモデル
	ctx   mtmd.Context // 読み込んだもの。0 ならまだ
	onCPU bool         // GPU で読めず CPU に載せた
	// loadSec は、まだ生成の記録に出していない読み込みの秒数(読み込んだ回の GenerateStats に1回だけ出す)
	loadSec float64
}

// withProjector は mmproj を(まだなら読み込んで)使う。
func (e *Engine) withProjector(f func(mtmd.Context) error) error {
	e.vision.mu.Lock()
	defer e.vision.mu.Unlock()
	if e.vision.ctx == 0 {
		if err := e.loadProjector(); err != nil {
			return err
		}
	}
	return f(e.vision.ctx)
}

// loadProjector は mmproj を読み込む。e.vision.mu を持った状態で呼ぶ。モデル(e.model)が読み込まれていること。
func (e *Engine) loadProjector() error {
	if e.vision.path == "" {
		return ErrNoVision
	}
	if e.mtmdErr != nil {
		return fmt.Errorf("画像を読むライブラリ(mtmd)を読み込めません。llama.cpp の配布物の mtmd.dll が AIBOW_LIB のフォルダにあるか確かめてください: %w", e.mtmdErr)
	}
	start := time.Now()
	p := mtmd.ContextParamsDefault()
	p.ImageMaxTokens = int32(e.cfg.imageMaxTokens())
	p.PrintTimings = false
	p.UseGPU = true
	ctx, err := mtmd.InitFromFile(e.vision.path, e.model, p)
	if err != nil || ctx == 0 {
		// GPU に載らない(VRAM が足りない)ときは CPU で読む。エンコードは遅くなるが、画像は読める
		p.UseGPU = false
		ctx, err = mtmd.InitFromFile(e.vision.path, e.model, p)
		if err != nil || ctx == 0 {
			return fmt.Errorf("画像を読む部品(mmproj)を読み込めません。AIBOW_LLAMA_LOG=1 で詳しい理由が出ます: %v", err)
		}
		e.vision.onCPU = true
	}
	e.vision.ctx = ctx
	e.vision.loadSec = time.Since(start).Seconds()
	return nil
}

// freeProjector は mmproj を解放する(モデルを降ろす・替えるとき)。
func (e *Engine) freeProjector() {
	e.vision.mu.Lock()
	defer e.vision.mu.Unlock()
	if e.vision.ctx != 0 {
		mtmd.Free(e.vision.ctx)
	}
	e.vision.ctx, e.vision.onCPU, e.vision.loadSec, e.vision.path = 0, false, 0, ""
}

// takeProjectorLoad は、まだ記録に出していない mmproj の読み込みの秒数と、CPU に載せたかを返す(読み込んだ回の記録に1回だけ出す)。
func (e *Engine) takeProjectorLoad() (sec float64, onCPU bool) {
	e.vision.mu.Lock()
	defer e.vision.mu.Unlock()
	sec, e.vision.loadSec = e.vision.loadSec, 0
	return sec, e.vision.onCPU && sec > 0
}

// input は、書式を当てた文字列 prompt をチャンクの列にする。画像が無ければ文字のチャンク1つ(今までと同じトークン列)。
// 画像があれば mtmd で、文字のチャンクと画像のチャンクの列にする(画像の印 <__media__> のところが画像になる)。
// placeholder なら画像の中身は読まず、大きさだけで数える(トークン数を数えるだけのとき。エンコードはできない)。
// 返した free は、生成が終わったら呼ぶ(画像のチャンクの元を解放する)。
func (e *Engine) input(p format.Prompt, prompt string, placeholder bool) ([]chunk, func(), error) {
	images := format.Images(p)
	if len(images) == 0 {
		return []chunk{textChunk(e.tokenize(prompt))}, func() {}, nil
	}
	if !format.ReadsImages(e.format) {
		return nil, nil, VisionError(fmt.Sprintf("今のモデルは画像に対応していません(書式 %s)。画像に対応したモデルに切り替えてください", e.format.Name()))
	}
	if n := strings.Count(prompt, format.ImageMarker); n != len(images) {
		return nil, nil, fmt.Errorf("画像が %d 枚なのに、プロンプトの画像の印(%s)が %d 個あります。発言の文に %s と書かれていないか確かめてください",
			len(images), format.ImageMarker, n, format.ImageMarker)
	}

	var out []chunk
	var chunks mtmd.InputChunks
	var bitmaps []mtmd.Bitmap
	free := func() {
		if chunks != 0 {
			mtmd.InputChunksFree(chunks)
		}
		for _, b := range bitmaps {
			mtmd.BitmapFree(b)
		}
	}
	err := e.withProjector(func(mctx mtmd.Context) error {
		for k, img := range images {
			if len(img.Data) == 0 {
				return fmt.Errorf("%d 枚目の画像が空です", k+1)
			}
			// jpg・png などの読み取り(と縮小)は llama.cpp がする
			b := mtmd.BitmapInitFromBuf(mctx, &img.Data[0], uint64(len(img.Data)), placeholder, mtmd.InitOptDefault())
			if b.Bitmap == 0 {
				return fmt.Errorf("%d 枚目の画像を読めません(PNG・JPEG・GIF・BMP などの画像ではない可能性があります)", k+1)
			}
			bitmaps = append(bitmaps, b.Bitmap)
			// KV キャッシュで「同じ画像か」を比べる ID。会話の仕組みが中身のハッシュを入れてくる(空なら mtmd が中身から作る)
			if img.ID != "" {
				mtmd.BitmapSetId(b.Bitmap, img.ID)
			}
		}
		chunks = mtmd.InputChunksInit()
		text := mtmd.NewInputText(strings.ToValidUTF8(prompt, replacementChar), true, true)
		if rc := mtmd.Tokenize(mctx, chunks, text, bitmaps); rc != 0 {
			return fmt.Errorf("画像の付いたプロンプトをトークンにできません(戻り値 %d)", rc)
		}
		for k := uint64(0); k < mtmd.InputChunksSize(chunks); k++ {
			c := mtmd.InputChunksGet(chunks, k)
			switch mtmd.InputChunkGetType(c) {
			case mtmd.InputChunkTypeImage:
				out = append(out, chunk{image: mtmd.InputChunkGetId(c), nTok: int(mtmd.InputChunkGetNTokens(c)), nPos: int(mtmd.InputChunkGetNPos(c)), src: c})
			case mtmd.InputChunkTypeText:
				// 返ってくるスライスは mtmd の中のメモリなので写す(KV キャッシュの記録に残すため)
				out = append(out, textChunk(append([]llama.Token(nil), mtmd.InputChunkGetTokensText(c)...)))
			default:
				return errors.New("音声のチャンクには対応していません")
			}
		}
		return nil
	})
	if err != nil {
		free()
		return nil, nil, err
	}
	return out, free, nil
}

// imageMaxTokens は画像1枚のトークン数の上限(0 以下なら既定)。
func (c Config) imageMaxTokens() int {
	if c.ImageMaxTokens > 0 {
		return c.ImageMaxTokens
	}
	return DefaultImageMaxTokens
}
