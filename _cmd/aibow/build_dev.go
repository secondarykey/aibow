//go:build !production

package main

// build_dev.go / build_production.go: dev で起動したビルドか(isDev)。
//
// リリースのビルド(wails3 build。Taskfile の build/windows/Taskfile.yml)は -tags production を付け、
// wails3 dev は付けない(Wails 自身もこのタグで dev とリリースを分けている)。
// そのタグをそのまま使い、Taskfile は変えない(wails3 のスキルの -tags dev の形ではなく、今ある production のタグに合わせた)。
// go vet / go run などタグ無しのビルドも dev になる。
const isDev = true
