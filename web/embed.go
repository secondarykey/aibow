// Package web は、画面(_cmd/aibow/frontend の React)のビルド結果を持つ(ステップ9。0016)。
//
// 画面の部品は、デスクトップのアプリとブラウザで同じものを使う(0016 の 2)。go:embed はパッケージのフォルダの下しか埋め込めないので、
// ビルド結果をどちらの exe のものでもないこのパッケージに出し、両方が import して埋め込む(写さない)。
//
//	_cmd/aibow/frontend(ソース)── vite build ──→ web/dist/app/(ビルド結果。Git には入れない)
//	                                                   ↑ import して埋め込む
//	                                   _cmd/aibow(Wails)と _cmd/aibow-server(ブラウザに配る)
//
// 画面をまだビルドしていなくても、ルートのモジュールの go vet / go test / go build が通るように、
// 「画面がビルドされていません」と出すだけのページ(dist/index.html)をコミットしてある。
package web

import (
	"embed"
	"io/fs"
)

// all: を付けないと、_ や . で始まるファイルが埋め込まれない(vite の出力に入ることがある)
//
//go:embed all:dist
var dist embed.FS

// Files は画面のファイル(index.html がいちばん上にある形)。built はビルドした画面か。
// ビルドしていなければ、ビルドの仕方を出すだけのページ(dist/index.html)。
func Files() (files fs.FS, built bool) {
	app, err := fs.Sub(dist, "dist/app")
	if err == nil {
		if _, err := fs.Stat(app, "index.html"); err == nil {
			return app, true
		}
	}
	files, _ = fs.Sub(dist, "dist") // dist は埋め込んであるので、失敗しない
	return files, false
}
