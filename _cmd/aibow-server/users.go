package main

// users.go: 利用者を作る・消す・パスワードを作り直すコマンド(0017 の決定事項 4。10-1)。
//
//	aibow-server [-config 設定ファイル] user list
//	aibow-server [-config 設定ファイル] user add <名前>      … 利用者を作り、本人がパスワードを決める招待のリンクを表示する(0022)
//	aibow-server [-config 設定ファイル] user passwd <名前>   … 今のパスワードを使えなくし、再設定のリンクを表示する(ログイン中の画面とアプリは切れる)
//	aibow-server [-config 設定ファイル] user remove <名前>   … 利用者と、その人の会話・プロファイル・設定を消す
//	aibow-server [-config 設定ファイル] user key <名前>      … API キーを作って表示する(1人1本。作り直すと前のキーは使えなくなる。10-2)
//	aibow-server [-config 設定ファイル] user admin <名前>    … 管理者にする(0018 の 11-0)
//	aibow-server [-config 設定ファイル] user unadmin <名前>  … 管理者から外す
//
// 管理者を決めるのはこのコマンドだけ(画面からは決めない。0018 の決定事項 6)。サーバーの PC でこのコマンドを打てる人が、いちばん上の管理者。
// 管理者が一人もいなくなっても止めない(127.0.0.1 からはいつも PC の事情を変えられ、このコマンドでいつでも戻せるため。0018 の決定事項 16)。
// llama.cpp もモデルも読み込まず、server.db だけを開く(aibow-server を動かしたままでも打てる。SQLite が順番に書く)。
//
// パスワードは本人が決める(0022。以前は aibow-server が作って一度だけ表示していた)。コマンドと管理者の画面は、パスワードを作らず、
// 本人がパスワードを決めるリンク(1 時間・一度だけ。auth の links.go)を出す。リンクは標準出力にだけ出し、ログには残さない。
// 入れなくなったら(管理者が自分のパスワードを忘れたなど)、サーバーの PC で passwd を打ってリンクを出し直す。
//
// 作る・再設定のリンクと API キーを作り直す・消すは、管理者の画面(admin.go。0018 の決定事項 7)からも同じ処理を使う
// (createUser・resetPassword・renewKey・deleteUser。by はログに足す「誰が」)。コマンドはその結果を標準出力に出すだけ。

import (
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"strconv"
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
		return addUser(ctx, st, initialSettings(env, 0), rest[0], linkBase(c))
	case "passwd":
		return passwdUser(ctx, st, rest[0], linkBase(c))
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

// link は、本人がパスワードを決めるリンク(合言葉と期限)。URL にするのは、出す側(コマンドは linkBase、画面は開いているアドレス)。
type link struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// addUser は利用者を作り、招待のリンクを表示する(コマンド)。
func addUser(ctx context.Context, st *store.SQLite, initial aibow.AppSettings, name string, base linkURL) error {
	l, err := createUser(ctx, st, initial, name, "")
	if err != nil {
		return err
	}
	printLink(name, "招待", l, base)
	return nil
}

// createUser は、パスワードの無い利用者を作り、その人のプロファイル「既定」を作る(0017 の決定事項 5。今の PrepareProfiles と同じ)。
// 本人がパスワードを決める招待のリンクを返す(0022)。リンクを使うまでログインできない。
func createUser(ctx context.Context, st *store.SQLite, initial aibow.AppSettings, name, by string) (link, error) {
	if err := checkName(name); err != nil {
		return link{}, err
	}
	now := time.Now()
	u := auth.User{ID: uuid.NewV7(), Name: name, CreatedAt: now, UpdatedAt: now}
	if err := st.CreateUser(ctx, u); err != nil {
		return link{}, fmt.Errorf("利用者 %s を作れません: %w", name, err)
	}
	// プロファイルの値は、新しい人の新しい会話の既定(サーバーの最後に使ったモデル・環境変数の初期値)から作る
	uctx := aibow.WithUser(ctx, u.ID)
	app, _, err := aibow.LoadAppSettings(uctx, st, initial)
	if err != nil {
		return link{}, err
	}
	if _, err := aibow.NewConversations(st, nil, app).PrepareProfiles(uctx); err != nil {
		return link{}, fmt.Errorf("利用者 %s のプロファイルを作れません: %w", name, err)
	}
	token, exp, err := auth.NewSessions(st).IssueLink(ctx, u.ID)
	if err != nil {
		return link{}, err
	}
	log.Noticef("利用者 %s を作りました%s(本人がパスワードを決める招待のリンクを出しました。期限 %s)", name, by, exp.Local().Format("15:04"))
	return link{Token: token, ExpiresAt: exp}, nil
}

// passwdUser は、今のパスワードを使えなくして、再設定のリンクを表示する(コマンド)。
func passwdUser(ctx context.Context, st *store.SQLite, name string, base linkURL) error {
	l, err := resetPassword(ctx, st, name, "")
	if err != nil {
		return err
	}
	printLink(name, "再設定", l, base)
	return nil
}

// resetPassword は、今のパスワードを使えなくし(ログイン中の画面とアプリ用のトークンも切れる)、本人が決め直す再設定のリンクを返す(0022 の決定事項 3)。
// パスワードを作り直すのは、忘れたか漏れたかもしれないときなので、リンクを使うまで前のパスワードは使わせない。
func resetPassword(ctx context.Context, st *store.SQLite, name, by string) (link, error) {
	u, err := st.UserByName(ctx, name)
	if err != nil {
		return link{}, fmt.Errorf("利用者 %s: %w", name, err)
	}
	if err := st.SetPassword(ctx, u.ID, "", time.Now()); err != nil {
		return link{}, err
	}
	if err := st.DeleteUserSessions(ctx, u.ID); err != nil {
		return link{}, err
	}
	token, exp, err := auth.NewSessions(st).IssueLink(ctx, u.ID)
	if err != nil {
		return link{}, err
	}
	log.Noticef("利用者 %s のパスワードを使えなくし、再設定のリンクを出しました%s(期限 %s。ログイン中の画面とアプリはログインし直しになります)", name, by, exp.Local().Format("15:04"))
	return link{Token: token, ExpiresAt: exp}, nil
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
	fmt.Println("API キーは今だけ表示します。Cline などは API Key の欄に書きます(デスクトップのアプリは設定の画面の「サーバー」でログインすれば要りません。settings.json に手で書いてつなぐときだけ serverKey に書きます)。" +
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

// linkURL は、コマンドが出すリンクの頭(http://127.0.0.1:8089 など)と、そのアドレスについての添え書き。
type linkURL struct {
	base, note string
}

// linkBase は、コマンドが出すリンクの頭を、aibow-server の待ち受け(引数・server.json・環境変数)から作る(0022 の決定事項 11)。
// 全部のネットワークで待ち受けるとき(0.0.0.0 など)は、この PC から開ける 127.0.0.1 にし、ほかの PC からはアドレスを置き換えるよう添える。
func linkBase(c config) linkURL {
	host, note := c.Host, ""
	if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
		host = "127.0.0.1"
		note = "ほかの PC で開くときは、127.0.0.1 をこの PC のアドレス(IP アドレスか名前)に置き換えてください"
	}
	return linkURL{base: c.Scheme() + "://" + net.JoinHostPort(host, strconv.Itoa(c.Port)), note: note}
}

// printLink は、パスワードを決めるリンクを標準出力にだけ出す(ログには残さない。持っている人なら誰でもパスワードを決められるため)。
func printLink(name, what string, l link, base linkURL) {
	fmt.Printf("名前: %s\n%sのリンク(%s まで・一度だけ): %s/#reset=%s\n", name, what, l.ExpiresAt.Local().Format("15:04"), base.base, l.Token)
	if base.note != "" {
		fmt.Println(base.note)
	}
	fmt.Println("このリンクを本人に渡し、ブラウザで開いてパスワードを決めてもらいます(12 文字以上)。期限が切れたら aibow-server user passwd " + name + " で出し直します")
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
