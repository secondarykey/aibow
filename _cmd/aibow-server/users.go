package main

// users.go: 利用者を作る・消す・パスワードを作り直すコマンド(0017 の決定事項 4。10-1)。
//
//	aibow-server [-config 設定ファイル] user list
//	aibow-server [-config 設定ファイル] user add <名前>      … 利用者を作り、パスワードを作って表示する
//	aibow-server [-config 設定ファイル] user passwd <名前>   … パスワードを作り直して表示する(ログイン中の画面はログインし直しになる)
//	aibow-server [-config 設定ファイル] user remove <名前>   … 利用者と、その人の会話・プロファイル・設定を消す
//	aibow-server [-config 設定ファイル] user key <名前>      … API キーを作って表示する(1人1本。作り直すと前のキーは使えなくなる。10-2)
//	aibow-server [-config 設定ファイル] user admin <名前>    … 管理者にする(0018 の 11-0)
//	aibow-server [-config 設定ファイル] user unadmin <名前>  … 管理者から外す
//
// 管理者を決めるのはこのコマンドだけ(画面からは決めない。0018 の決定事項 6)。サーバーの PC でこのコマンドを打てる人が、いちばん上の管理者。
// 管理者が一人もいなくなっても止めない(127.0.0.1 からはいつも PC の事情を変えられ、このコマンドでいつでも戻せるため。0018 の決定事項 16)。
// llama.cpp もモデルも読み込まず、server.db だけを開く(aibow-server を動かしたままでも打てる。SQLite が順番に書く)。
//
// パスワードは aibow-server が作って一度だけ表示する(ユーザーと決めた)。表示はログに残さず、画面(標準出力)にだけ出す。
// 打ったパスワードがターミナルに残らず、短いパスワードにもならない。忘れたら passwd で作り直す。
//
// 作る・パスワードと API キーを作り直す・消すは、管理者の画面(admin.go。0018 の決定事項 7)からも同じ処理を使う
// (createUser・renewPassword・renewKey・deleteUser。by はログに足す「誰が」)。コマンドはその結果を標準出力に出すだけ。

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
	"uuid"

	"github.com/secondarykey/aibow"
	"github.com/secondarykey/aibow/auth"
	"github.com/secondarykey/aibow/log"
	"github.com/secondarykey/aibow/store"
)

const userUsage = `使い方:
  aibow-server [-config 設定ファイル] user list
  aibow-server [-config 設定ファイル] user add <名前>
  aibow-server [-config 設定ファイル] user passwd <名前>
  aibow-server [-config 設定ファイル] user remove <名前>
  aibow-server [-config 設定ファイル] user key <名前>
  aibow-server [-config 設定ファイル] user admin <名前>
  aibow-server [-config 設定ファイル] user unadmin <名前>`

// runUser は user のコマンドを実行する。args は user のあと。
func runUser(c config, env *envReader, args []string) error {
	if len(args) == 0 {
		return errors.New(userUsage)
	}
	cmd, rest := args[0], args[1:]
	want := 1
	if cmd == "list" {
		want = 0
	}
	if len(rest) != want {
		return fmt.Errorf("引数の数が違います\n%s", userUsage)
	}
	st, err := store.Open(c.DBPath)
	if err != nil {
		return err
	}
	defer st.Close()
	ctx := context.Background()

	switch cmd {
	case "list":
		return listUsers(ctx, st)
	case "add":
		return addUser(ctx, st, initialSettings(env, 0), rest[0])
	case "passwd":
		return resetPassword(ctx, st, rest[0])
	case "remove":
		return deleteUser(ctx, st, rest[0], "")
	case "key":
		return newKey(ctx, st, rest[0])
	case "admin":
		return setAdmin(ctx, st, rest[0], true)
	case "unadmin":
		return setAdmin(ctx, st, rest[0], false)
	}
	return fmt.Errorf("知らないコマンドです: user %s\n%s", cmd, userUsage)
}

func listUsers(ctx context.Context, st *store.SQLite) error {
	list, err := st.Users(ctx)
	if err != nil {
		return err
	}
	for _, u := range list {
		pw := "パスワードあり"
		if u.Password == "" {
			pw = "パスワードなし(ログインできない)"
		}
		note := ""
		if has, err := st.HasAPIKey(ctx, u.ID); err != nil {
			return err
		} else if has {
			note = "・API キーあり"
		}
		if u.Admin {
			note += "・管理者"
		}
		if u.ID == aibow.LocalUser {
			note += "・ログインなしの形の利用者"
		}
		fmt.Printf("%s\t%s%s\t作った日時 %s\n", u.Name, pw, note, u.CreatedAt.Format("2006-01-02 15:04"))
	}
	return nil
}

// addUser は利用者を作り、パスワードを表示する(コマンド)。
func addUser(ctx context.Context, st *store.SQLite, initial aibow.AppSettings, name string) error {
	password, err := createUser(ctx, st, initial, name, "")
	if err != nil {
		return err
	}
	printPassword(name, password)
	return nil
}

// createUser は利用者を作り、その人のプロファイル「既定」を作る(0017 の決定事項 5。今の PrepareProfiles と同じ)。作ったパスワードを返す。
func createUser(ctx context.Context, st *store.SQLite, initial aibow.AppSettings, name, by string) (string, error) {
	if err := checkName(name); err != nil {
		return "", err
	}
	password := auth.NewPassword()
	hash, err := auth.HashPassword(password)
	if err != nil {
		return "", err
	}
	now := time.Now()
	u := auth.User{ID: uuid.NewV7(), Name: name, Password: hash, CreatedAt: now, UpdatedAt: now}
	if err := st.CreateUser(ctx, u); err != nil {
		return "", fmt.Errorf("利用者 %s を作れません: %w", name, err)
	}
	// プロファイルの値は、新しい人の新しい会話の既定(サーバーの最後に使ったモデル・環境変数の初期値)から作る
	uctx := aibow.WithUser(ctx, u.ID)
	app, _, err := aibow.LoadAppSettings(uctx, st, initial)
	if err != nil {
		return "", err
	}
	if _, err := aibow.NewConversations(st, nil, app).PrepareProfiles(uctx); err != nil {
		return "", fmt.Errorf("利用者 %s のプロファイルを作れません: %w", name, err)
	}
	log.Noticef("利用者 %s を作りました%s", name, by)
	return password, nil
}

// resetPassword はパスワードを作り直して表示する(コマンド)。
func resetPassword(ctx context.Context, st *store.SQLite, name string) error {
	password, err := renewPassword(ctx, st, name, "")
	if err != nil {
		return err
	}
	printPassword(name, password)
	return nil
}

// renewPassword はパスワードを作り直し、新しいパスワードを返す。
func renewPassword(ctx context.Context, st *store.SQLite, name, by string) (string, error) {
	u, err := st.UserByName(ctx, name)
	if err != nil {
		return "", fmt.Errorf("利用者 %s: %w", name, err)
	}
	password := auth.NewPassword()
	hash, err := auth.HashPassword(password)
	if err != nil {
		return "", err
	}
	if err := st.SetPassword(ctx, u.ID, hash, time.Now()); err != nil {
		return "", err
	}
	// 前のパスワードでログインしていた画面は使えなくする(パスワードを作り直すのは、漏れたかもしれないときなので)
	if err := st.DeleteUserSessions(ctx, u.ID); err != nil {
		return "", err
	}
	log.Noticef("利用者 %s のパスワードを作り直しました%s(ログイン中の画面はログインし直しになります)", name, by)
	return password, nil
}

// deleteUser は利用者と、その人の会話・プロファイル・設定を消す。
func deleteUser(ctx context.Context, st *store.SQLite, name, by string) error {
	u, err := st.UserByName(ctx, name)
	if err != nil {
		return fmt.Errorf("利用者 %s: %w", name, err)
	}
	if u.ID == aibow.LocalUser {
		return fmt.Errorf("%s はログインなしの形の利用者なので消せません(ログインなしで使うときの会話の持ち主です)", name)
	}
	if err := st.DeleteUser(ctx, u.ID); err != nil {
		return err
	}
	log.Noticef("利用者 %s と、その人の会話・プロファイル・設定を消しました%s", name, by)
	return nil
}

// newKey は API キーを作って表示する(コマンド)。表示はこのときだけ(保存するのはハッシュだけなので、あとからは見られない)。
func newKey(ctx context.Context, st *store.SQLite, name string) error {
	key, err := renewKey(ctx, st, name, "")
	if err != nil {
		return err
	}
	fmt.Printf("名前: %s\nAPI キー: %s\n", name, key)
	fmt.Println("API キーは今だけ表示します。デスクトップのアプリは settings.json の serverKey に、Cline などは API Key の欄に書きます。" +
		"忘れたら aibow-server user key " + name + " で作り直してください(前のキーは使えなくなります)")
	return nil
}

// renewKey は API キーを作り(前のキーは使えなくなる)、新しいキーを返す。
func renewKey(ctx context.Context, st *store.SQLite, name, by string) (string, error) {
	u, err := st.UserByName(ctx, name)
	if err != nil {
		return "", fmt.Errorf("利用者 %s: %w", name, err)
	}
	key := auth.NewAPIKey()
	if err := st.SetAPIKey(ctx, u.ID, auth.KeyID(key), time.Now()); err != nil {
		return "", err
	}
	log.Noticef("利用者 %s の API キーを作りました%s(前のキーは使えなくなりました)", name, by)
	return key, nil
}

// setAdmin は管理者の印を付ける / 外す。外して一人もいなくなったら知らせる(止めはしない。0018 の決定事項 16)。
func setAdmin(ctx context.Context, st *store.SQLite, name string, admin bool) error {
	u, err := st.UserByName(ctx, name)
	if err != nil {
		return fmt.Errorf("利用者 %s: %w", name, err)
	}
	if u.Admin == admin {
		if admin {
			fmt.Println(name, "はもう管理者です")
		} else {
			fmt.Println(name, "は管理者ではありません")
		}
		return nil
	}
	if err := st.SetAdmin(ctx, u.ID, admin, time.Now()); err != nil {
		return err
	}
	if admin {
		log.Noticef("利用者 %s を管理者にしました", name)
		return nil
	}
	log.Noticef("利用者 %s を管理者から外しました", name)
	list, err := st.Users(ctx)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(list, func(u auth.User) bool { return u.Admin }) {
		log.Warn("管理者が一人もいなくなりました。サーバーの PC の事情(モデル・層の数・置き場所)は 127.0.0.1 からだけ変えられます。" +
			"戻すときは aibow-server user admin <名前> を実行してください")
	}
	return nil
}

// printPassword はパスワードを標準出力にだけ出す(ログには残さない)。
func printPassword(name, password string) {
	fmt.Printf("名前: %s\nパスワード: %s\n", name, password)
	fmt.Println("パスワードは今だけ表示します。忘れたら aibow-server user passwd " + name + " で作り直してください")
}

// checkName は、利用者の名前に使えるか(ログインの画面で打つので、空白や見えない文字を入れない)。
func checkName(name string) error {
	if name == "" || utf8.RuneCountInString(name) > 32 {
		return errors.New("名前は 1〜32 文字にしてください")
	}
	if strings.IndexFunc(name, func(r rune) bool { return unicode.IsSpace(r) || !unicode.IsPrint(r) }) >= 0 {
		return fmt.Errorf("名前に空白や見えない文字は使えません: %q", name)
	}
	return nil
}
