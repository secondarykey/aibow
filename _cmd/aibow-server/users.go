package main

// users.go: 利用者を作る・消す・パスワードを作り直すコマンド(0017 の決定事項 4。10-1)。
//
//	aibow-server [-config 設定ファイル] user list
//	aibow-server [-config 設定ファイル] user add <名前>      … 利用者を作り、本人がパスワードを決める招待のリンクを表示する(0022)
//	aibow-server [-config 設定ファイル] user passwd <名前>   … 今のパスワードを使えなくし、再設定のリンクを表示する(ログイン中の画面とアプリは切れる)
//	aibow-server [-config 設定ファイル] user remove <名前>   … 利用者と、その人の会話・プロファイル・設定を消す
//	aibow-server [-config 設定ファイル] user key <名前> [日数] … API キーを作って表示する(1人1本。作り直すと前のキーは使えなくなる。10-2)。
//	                                                         日数は期限で 30・90・365・0(無期限)。省けば 90(0060)
//	aibow-server [-config 設定ファイル] user unkey <名前>    … API キーを削除する(作り直さずに止める。0060)
//	aibow-server [-config 設定ファイル] user disable <名前>  … 利用者を無効にする(消さずに止める。ログイン中の画面とアプリは切れる。会話と API キーは残す。0060)
//	aibow-server [-config 設定ファイル] user enable <名前>   … 無効にした利用者を有効に戻す
//	aibow-server [-config 設定ファイル] user admin <名前>    … 管理者にする(0018 の 11-0)
//	aibow-server [-config 設定ファイル] user unadmin <名前>  … 管理者から外す
//	aibow-server [-config 設定ファイル] user display <名前> [表示名] … 表示名を決める(表示名を省くと無しにする。0031)
//	aibow-server [-config 設定ファイル] user email <名前> [アドレス]  … メールアドレスを決める(アドレスを省くと無しにする。0031)
//
// 管理者を決めるのはこのコマンドだけ(画面からは決めない。0018 の決定事項 6)。サーバーの PC でこのコマンドを打てる人が、いちばん上の管理者。
// 管理者が一人もいなくなっても止めない(127.0.0.1 からはいつも PC の事情を変えられ、このコマンドでいつでも戻せるため。0018 の決定事項 16)。
// llama.cpp もモデルも読み込まず、server.db だけを開く(aibow-server を動かしたままでも打てる。SQLite が順番に書く)。
//
// パスワードは本人が決める(0022。以前は aibow-server が作って一度だけ表示していた)。コマンドと管理者の画面は、パスワードを作らず、
// 本人がパスワードを決めるリンク(1 時間・一度だけ。auth の links.go)を出す。リンクは標準出力にだけ出し、ログには残さない。
// 入れなくなったら(管理者が自分のパスワードを忘れたなど)、サーバーの PC で passwd を打ってリンクを出し直す。
//
// 作る・再設定のリンクと API キーを作り直す・消す・無効にするは、管理者の画面(admin.go。0018 の決定事項 7)からも同じ処理を使う
// (createUser・resetPassword・renewKey・removeKey・setDisabled・deleteUser。by はログに足す「誰が」)。コマンドはその結果を標準出力に出すだけ。

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
	"github.com/secondarykey/aibow/config"
	"github.com/secondarykey/aibow/log"
	"github.com/secondarykey/aibow/store"
)

const userUsage = `使い方:
  aibow-server [-config 設定ファイル] user list
  aibow-server [-config 設定ファイル] user add <名前>
  aibow-server [-config 設定ファイル] user passwd <名前>
  aibow-server [-config 設定ファイル] user remove <名前>
  aibow-server [-config 設定ファイル] user key <名前> [日数(30・90・365・0 = 無期限。省けば 90)]
  aibow-server [-config 設定ファイル] user unkey <名前>
  aibow-server [-config 設定ファイル] user disable <名前>
  aibow-server [-config 設定ファイル] user enable <名前>
  aibow-server [-config 設定ファイル] user admin <名前>
  aibow-server [-config 設定ファイル] user unadmin <名前>
  aibow-server [-config 設定ファイル] user display <名前> [表示名]
  aibow-server [-config 設定ファイル] user email <名前> [アドレス]`

// runUser は user のコマンドを実行する。args は user のあと。
func runUser(c serverConfig, env *config.Env, args []string) error {
	if len(args) == 0 {
		return errors.New(userUsage)
	}
	cmd, rest := args[0], args[1:]
	want := 1
	if cmd == "list" {
		want = 0
	}
	// display は表示名を省ける(省くと無しにする)。空白を含む表示名は引用符で囲む
	if (cmd == "display" || cmd == "email") && len(rest) == 2 {
		want = 2
	}
	// key は期限の日数を省ける(省くと 90 日)
	days := auth.DefaultKeyDays
	if cmd == "key" && len(rest) == 2 {
		want = 2
		n, err := strconv.Atoi(rest[1])
		if err != nil {
			return fmt.Errorf("期限は日数(30・90・365・0 = 無期限)で書いてください: %q", rest[1])
		}
		days = n
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
		return newKey(ctx, st, rest[0], days)
	case "unkey":
		if err := removeKey(ctx, st, rest[0], ""); err != nil {
			return err
		}
		fmt.Println(rest[0], "の API キーを削除しました(そのキーはもう使えません)")
		return nil
	case "disable":
		return setDisabled(ctx, st, rest[0], true, "", "")
	case "enable":
		return setDisabled(ctx, st, rest[0], false, "", "")
	case "admin":
		return setAdmin(ctx, st, rest[0], true)
	case "unadmin":
		return setAdmin(ctx, st, rest[0], false)
	case "display":
		return setDisplayName(ctx, st, rest[0], strings.Join(rest[1:], ""), "")
	case "email":
		return setEmail(ctx, st, rest[0], strings.Join(rest[1:], ""), "")
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
		if u.Disabled {
			note = "・無効"
		}
		if has, exp, err := st.HasAPIKey(ctx, u.ID); err != nil {
			return err
		} else if has {
			note += "・API キーあり(" + keyState(exp, time.Now()) + ")"
		}
		if u.Admin {
			note += "・管理者"
		}
		if u.ID == aibow.LocalUser {
			note += "・ログインなしで使うときの利用者"
		}
		if u.Email != "" {
			note += "・" + u.Email
		}
		name := u.Name
		if u.DisplayName != "" {
			name = fmt.Sprintf("%s(%s)", u.Name, u.DisplayName)
		}
		used := "使っていない"
		if !u.LastUsedAt.IsZero() {
			used = u.LastUsedAt.Format("2006-01-02")
		}
		fmt.Printf("%s\t%s%s\t作った日時 %s\t最後に使った日 %s\n", name, pw, note, u.CreatedAt.Format("2006-01-02 15:04"), used)
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
	log.Noticef("利用者 %s を作成しました%s(本人がパスワードを設定する招待のリンクを発行しました。期限 %s)", name, by, exp.Local().Format("15:04"))
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
		return fmt.Errorf("%s はログインなしで使うときの利用者なので削除できません(ログインなしで使うときの会話の持ち主です)", name)
	}
	if err := st.DeleteUser(ctx, u.ID); err != nil {
		return err
	}
	log.Noticef("利用者 %s と、その人の会話・プロファイル・設定を削除しました%s", name, by)
	return nil
}

// newKey は API キーを作って表示する(コマンド)。表示はこのときだけ(保存するのはハッシュだけなので、あとからは見られない)。
func newKey(ctx context.Context, st *store.SQLite, name string, days int) error {
	key, exp, err := renewKey(ctx, st, name, days, "")
	if err != nil {
		return err
	}
	fmt.Printf("名前: %s\nAPI キー: %s\n期限: %s\n", name, key, keyUntil(exp))
	fmt.Println("API キーは今だけ表示します。Cline などは API Key の欄に書きます(デスクトップのアプリは設定の画面の「サーバー」でログインすれば要りません。settings.json に手で書いてつなぐときだけ serverKey に書きます)。" +
		"忘れたら aibow-server user key " + name + " で作り直してください(前のキーは使えなくなります)")
	return nil
}

// renewKey は、期限が days 日(0 なら無期限)の API キーを作り(前のキーは使えなくなる)、新しいキーと期限(無期限ならゼロ)を返す。
func renewKey(ctx context.Context, st *store.SQLite, name string, days int, by string) (string, time.Time, error) {
	now := time.Now()
	exp, err := auth.KeyExpires(now, days)
	if err != nil {
		return "", time.Time{}, err
	}
	u, err := st.UserByName(ctx, name)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("利用者 %s: %w", name, err)
	}
	key := auth.NewAPIKey()
	if err := st.SetAPIKey(ctx, u.ID, auth.KeyID(key), now, exp); err != nil {
		return "", time.Time{}, err
	}
	log.Noticef("利用者 %s の API キーを作りました%s(期限: %s。前のキーは使えなくなりました)", name, by, keyUntil(exp))
	return key, exp, nil
}

// removeKey は API キーを削除する(作り直さずに止める。0060)。キーが無ければ auth.ErrNoAPIKey。
func removeKey(ctx context.Context, st *store.SQLite, name, by string) error {
	u, err := st.UserByName(ctx, name)
	if err != nil {
		return fmt.Errorf("利用者 %s: %w", name, err)
	}
	if err := st.DeleteAPIKey(ctx, u.ID); err != nil {
		return fmt.Errorf("利用者 %s: %w", name, err)
	}
	log.Noticef("利用者 %s の API キーを削除しました%s", name, by)
	return nil
}

// keyUntil は API キーの期限の見せ方(「2027-02-21 まで」/「無期限」)。
func keyUntil(exp time.Time) string {
	if exp.IsZero() {
		return "無期限"
	}
	return exp.Local().Format("2006-01-02") + " まで"
}

// keyState は一覧に出す API キーの様子(期限が過ぎていれば「期限切れ」)。
func keyState(exp, now time.Time) string {
	if !exp.IsZero() && !now.Before(exp) {
		return "期限切れ"
	}
	return keyUntil(exp)
}

// cannotDisable は、無効にできない人(自分と local。0060 の決定事項 19)を無効にしようとしたときのエラー(管理の画面は 400 で返す)
type cannotDisable string

func (e cannotDisable) Error() string { return string(e) }

// setDisabled は利用者を無効にする / 有効に戻す(0060)。self は呼んだ管理者の名前(コマンドなら空)で、自分は無効にできない。
// ログインなしで使うときの利用者(local)も無効にできない(ログインなしで使うときの会話の持ち主なので)。管理者を無効にするのは止めない(0018 の決定事項 16 と同じ考え方)。
// 無効にすると、その人のログイン中の画面とアプリはすぐ切れる(store がセッションなどを消す)。会話・プロファイル・API キーは残す。
func setDisabled(ctx context.Context, st *store.SQLite, name string, disabled bool, self, by string) error {
	u, err := st.UserByName(ctx, name)
	if err != nil {
		return fmt.Errorf("利用者 %s: %w", name, err)
	}
	if disabled && u.ID == aibow.LocalUser {
		return cannotDisable(name + " はログインなしで使うときの利用者なので無効にできません(ログインなしで使うときの会話の持ち主です)")
	}
	if disabled && name == self {
		// 自分を無効にすると、そのまま画面から締め出される
		return cannotDisable("自分は無効にできません")
	}
	if u.Disabled == disabled {
		if by == "" {
			if disabled {
				fmt.Println(name, "はもう無効です")
			} else {
				fmt.Println(name, "は無効ではありません")
			}
		}
		return nil
	}
	if err := st.SetDisabled(ctx, u.ID, disabled, time.Now()); err != nil {
		return err
	}
	if disabled {
		log.Noticef("利用者 %s を無効にしました%s(ログイン中の画面とアプリは切れました。会話・プロファイル・API キーは残しています)", name, by)
	} else {
		log.Noticef("利用者 %s を有効に戻しました%s", name, by)
	}
	return nil
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
		log.Warn("管理者が一人もいなくなりました。モデル・GPU の層数・置き場所・ツールの既定は、管理者を決めるまで誰も変えられません(0046)。" +
			"戻すときは aibow-server user admin <名前> を実行してください")
	}
	return nil
}

// setDisplayName は表示名を決める(コマンドと管理者の画面。0031 の決定事項 19)。空なら無し(ログインの名前を出す)。
// ログはログインの名前で書く(表示名は変わるため。決定事項 20)。
func setDisplayName(ctx context.Context, st *store.SQLite, name, display, by string) error {
	display, err := auth.CheckDisplayName(display)
	if err != nil {
		return err
	}
	u, err := st.UserByName(ctx, name)
	if err != nil {
		return fmt.Errorf("利用者 %s: %w", name, err)
	}
	if err := st.SetDisplayName(ctx, u.ID, display, time.Now()); err != nil {
		return err
	}
	log.Noticef("利用者 %s の表示名を %q にしました%s", name, display, by)
	return nil
}

// setEmail はメールアドレスを決める(コマンドと管理者の画面。0031 の決定事項 40〜43)。空なら無し。
// アドレスそのものはログに書かない(個人の連絡先なので。名前だけ)。
func setEmail(ctx context.Context, st *store.SQLite, name, email, by string) error {
	email, err := auth.CheckEmail(email)
	if err != nil {
		return err
	}
	u, err := st.UserByName(ctx, name)
	if err != nil {
		return fmt.Errorf("利用者 %s: %w", name, err)
	}
	if err := st.SetEmail(ctx, u.ID, email, time.Now()); err != nil {
		return err
	}
	if email == "" {
		log.Noticef("利用者 %s のメールアドレスを無しにしました%s", name, by)
	} else {
		log.Noticef("利用者 %s のメールアドレスを決めました%s", name, by)
	}
	return nil
}

// linkURL は、コマンドが出すリンクの頭(http://127.0.0.1:8089 など)と、そのアドレスについての添え書き。
type linkURL struct {
	base, note string
}

// linkBase は、コマンドが出すリンクの頭を、aibow-server の待ち受け(引数・server.json・環境変数)から作る(0022 の決定事項 11)。
// 全部のネットワークで待ち受けるとき(0.0.0.0 など)は、この PC から開ける 127.0.0.1 にし、ほかの PC からはアドレスを置き換えるよう添える。
func linkBase(c serverConfig) linkURL {
	host, note := c.Host, ""
	if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
		host = "127.0.0.1"
		note = "ほかの PC で開くときは、127.0.0.1 をこの PC のアドレス(IP アドレスか名前)に置き換えてください"
	}
	// 道の頭(base。0042)も付ける。画面は base の下にあるので、付けないとリンクが 404 になる
	return linkURL{base: c.Scheme() + "://" + net.JoinHostPort(host, strconv.Itoa(c.Port)) + c.Base, note: note}
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
