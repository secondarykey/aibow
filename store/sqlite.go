// Package store は、会話を SQLite に保存する部分(aibow.Store の実装)。
//
// SQLite は1つのファイルに全部が入る小さなデータベース。サーバーを立てずに、
// アプリの中から SQL で読み書きできる。ドライバーの modernc.org/sqlite は SQLite の C のコードを
// Go に変換したもので、C コンパイラ(cgo)なしでビルドできる。
//
// Go からは標準の database/sql を通して使う。ドライバーを import すると "sqlite" という名前で登録され、
// sql.Open("sqlite", …) で開けるようになる。
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
	"uuid"

	"github.com/secondarykey/aibow"

	_ "modernc.org/sqlite" // "sqlite" ドライバーを登録する
)

// SQLite は aibow.Store を SQLite で実装したもの。
//
// 会話・プロファイル・人ごとの設定には持ち主(owner。利用者の ID)の列があり、ctx の利用者(aibow.UserOf)の行だけを読み書きする(0017)。
// 発言と要約は会話を通して持ち主が決まるので、列を持たず、会話の持ち主で確かめる。
type SQLite struct {
	db *sql.DB
	// oneUser は、ctx に利用者が無い呼び出しを、ログインなしの形の利用者(aibow.LocalUser)のものとして扱うか(OneUser)
	oneUser bool
}

var _ aibow.Store = (*SQLite)(nil) // aibow.Store のメソッドを全部持っているか、コンパイル時に確かめる
var _ aibow.ConversationLocks = (*SQLite)(nil)

// Open は DB ファイルを開く(無ければ作る)。テーブルが古ければ作り変える(マイグレーション)。
func Open(path string) (*SQLite, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("DB のフォルダを作れません: %w", err)
	}
	// _pragma は、接続を開くたびに実行する設定(modernc.org/sqlite の書き方)。
	//   foreign_keys(1) : REFERENCES を効かせる。SQLite は既定で無効なので、
	//                     これが無いと ON DELETE CASCADE(会話を消すと発言も消える)が動かない
	//   busy_timeout    : 別の接続が書き込み中なら、エラーにせず最大 5 秒待つ
	//   secure_delete(1): 消した行の場所をゼロで上書きする。SQLite は既定では行を消しても場所に「空き」の印を付けるだけで、
	//                     読み込んだ画像やファイル(0030)の中身が、上書きされるか VACUUM するまで DB ファイルの中に残るため
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=secure_delete(1)")
	if err != nil {
		return nil, fmt.Errorf("DB を開けません(%s): %w", path, err)
	}
	// 書き込みは1つずつしかできないので、接続も1つにして順番待ちさせる
	db.SetMaxOpenConns(1)

	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("DB を準備できません(%s): %w", path, err)
	}
	return &SQLite{db: db}, nil
}

func (s *SQLite) Close() error {
	return s.db.Close()
}

// OneUser は、ctx に利用者が無い呼び出しを、ログインなしの形の利用者(aibow.LocalUser)のものとして扱うようにする。
// デスクトップのアプリの手元の DB(aibow.db)のため。使うのはその PC の1人だけで、利用者の印を付ける HTTP の層が無い。
// 呼ばなければ、利用者が無い呼び出しは aibow.ErrNoUser で断る(aibow-server の server.db。印を付け忘れたときに、ほかの人の会話を見せないため)。
// Open の直後、使い始める前に呼ぶ。
func (s *SQLite) OneUser() {
	s.oneUser = true
}

// owner は、ctx の利用者(読み書きする行の持ち主)を、保存する形(UUID の文字列)で返す。
func (s *SQLite) owner(ctx context.Context) (string, error) {
	if id, ok := aibow.UserOf(ctx); ok {
		return id.String(), nil
	}
	if s.oneUser {
		return aibow.LocalUser.String(), nil
	}
	return "", aibow.ErrNoUser
}

// isAdmin は、利用者 me が管理者か(利用者の表にいなければ false。デスクトップのアプリの aibow.db では、いつも false)。
func (s *SQLite) isAdmin(ctx context.Context, me string) (int, error) {
	var admin int
	err := s.db.QueryRowContext(ctx, "SELECT admin FROM users WHERE id = ?", me).Scan(&admin)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return admin, err
}

// ---- 値の変換 ---------------------------------------------------------------------------
//
// ID は TEXT(UUID の文字列)、日時は TEXT(UTC、桁数固定)で保存する。DB を直接のぞいても読めるように。
// 日時の桁数を固定しているのは、文字列の並び順 = 時刻の並び順にするため(ORDER BY updated_at)。

const timeFormat = "2006-01-02T15:04:05.000000Z"

func formatTime(t time.Time) string { return t.UTC().Format(timeFormat) }

func parseTime(s string) (time.Time, error) {
	t, err := time.Parse(timeFormat, s)
	return t.Local(), err
}

// uuid.UUID は database/sql の Scan / Value を持たないので、文字列を経由して変換する
func parseID(s string) (uuid.UUID, error) {
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("ID が UUID の形ではありません(%q): %w", s, err)
	}
	return id, nil
}

// ---- ファイルをまたいで使う小さな部品 ------------------------------------------------

// rowScanner は *sql.Row と *sql.Rows の共通部分(1行を変数に読み込む)。
type rowScanner interface {
	Scan(dest ...any) error
}

// notFoundIfNone は、書き換えた行が無ければ notFound を返す(無いか、その人のものでない)。
func notFoundIfNone(res sql.Result, err error, notFound error) error {
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return notFound
	}
	return nil
}
