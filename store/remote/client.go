package remote

// client.go: aibow.Store の HTTP 版(画面のアプリが aibow-server につないだときの保存先)。
//
// メソッドは1つずつ、名前と引数をそのまま保存の口に送るだけ(会話の仕組みの判断はしない)。
// つなぎ方(つなぎ先・API キー・証明書・つながらないときのエラー)は、生成と同じ api/client の Client を使う。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"uuid"

	"github.com/secondarykey/aibow"
	apiclient "github.com/secondarykey/aibow/api/client"
)

// Client は aibow.Store と aibow.ConversationLocks の HTTP 版。
type Client struct {
	c *apiclient.Client
}

var _ Backend = (*Client)(nil)

// New は、生成と同じつなぎ先(api/client)で保存も頼む Client を作る。ここではまだつながない。
func New(c *apiclient.Client) *Client { return &Client{c: c} }

// call は、保存の口のメソッド name を args で呼び、戻り値を outs に読む(outs は戻り値の順。error は除く)。
// サーバーが code で知らせたエラーは、手元と同じエラー(aibow.ErrNotFound など)にする。
func (c *Client) call(ctx context.Context, name string, args []any, outs ...any) error {
	if args == nil {
		args = []any{}
	}
	var raw []json.RawMessage
	if err := c.c.Post(ctx, "/aibow/store/"+name, args, &raw); err != nil {
		var ae *apiclient.APIError
		if errors.As(err, &ae) {
			if known := errorOf(ae.Code); known != nil {
				return known
			}
		}
		return err
	}
	if len(raw) != len(outs) {
		return fmt.Errorf("保存の口 %s の返事の数が違います(%d 個。期待 %d 個)。aibow-server とアプリの版が違うかもしれません", name, len(raw), len(outs))
	}
	for i, o := range outs {
		if err := json.Unmarshal(raw[i], o); err != nil {
			return fmt.Errorf("保存の口 %s の返事が読めません: %w", name, err)
		}
	}
	return nil
}

// ---- 会話 -------------------------------------------------------------------------------

func (c *Client) ListConversations(ctx context.Context) (out []aibow.Conversation, err error) {
	err = c.call(ctx, "ListConversations", nil, &out)
	return out, err
}

func (c *Client) Conversation(ctx context.Context, id uuid.UUID) (conv aibow.Conversation, ms []aibow.StoredMessage, err error) {
	err = c.call(ctx, "Conversation", []any{id}, &conv, &ms)
	return conv, ms, err
}

func (c *Client) CreateConversation(ctx context.Context, conv aibow.Conversation) error {
	return c.call(ctx, "CreateConversation", []any{conv})
}

func (c *Client) UpdateConversation(ctx context.Context, conv aibow.Conversation) error {
	return c.call(ctx, "UpdateConversation", []any{conv})
}

func (c *Client) TouchConversation(ctx context.Context, id uuid.UUID, at time.Time, autoTitle string) error {
	return c.call(ctx, "TouchConversation", []any{id, at, autoTitle})
}

func (c *Client) DeleteConversation(ctx context.Context, id uuid.UUID) error {
	return c.call(ctx, "DeleteConversation", []any{id})
}

func (c *Client) AddMessage(ctx context.Context, m aibow.StoredMessage) error {
	return c.call(ctx, "AddMessage", []any{m})
}

func (c *Client) SetMessagesExcluded(ctx context.Context, ids []uuid.UUID, excluded bool) error {
	return c.call(ctx, "SetMessagesExcluded", []any{ids, excluded})
}

func (c *Client) DeleteMessages(ctx context.Context, ids []uuid.UUID) error {
	return c.call(ctx, "DeleteMessages", []any{ids})
}

func (c *Client) AddCompaction(ctx context.Context, comp aibow.Compaction) error {
	return c.call(ctx, "AddCompaction", []any{comp})
}

func (c *Client) Compactions(ctx context.Context, conv uuid.UUID) (out []aibow.Compaction, err error) {
	err = c.call(ctx, "Compactions", []any{conv}, &out)
	return out, err
}

// ---- プロファイル -------------------------------------------------------------------------

func (c *Client) ListProfiles(ctx context.Context) (out []aibow.Profile, err error) {
	err = c.call(ctx, "ListProfiles", nil, &out)
	return out, err
}

func (c *Client) Profile(ctx context.Context, id uuid.UUID) (p aibow.Profile, err error) {
	err = c.call(ctx, "Profile", []any{id}, &p)
	return p, err
}

func (c *Client) CreateProfile(ctx context.Context, p aibow.Profile) error {
	return c.call(ctx, "CreateProfile", []any{p})
}

func (c *Client) UpdateProfile(ctx context.Context, p aibow.Profile) error {
	return c.call(ctx, "UpdateProfile", []any{p})
}

func (c *Client) DeleteProfile(ctx context.Context, id uuid.UUID) error {
	return c.call(ctx, "DeleteProfile", []any{id})
}

// ---- 設定 -------------------------------------------------------------------------------
//
// アプリ全体の設定は、サーバーではその人の設定として置かれる(handler.go の appAsUser)。

func (c *Client) AppSetting(ctx context.Context, key string) (v string, ok bool, err error) {
	err = c.call(ctx, "AppSetting", []any{key}, &v, &ok)
	return v, ok, err
}

func (c *Client) SetAppSetting(ctx context.Context, key, value string) error {
	return c.call(ctx, "SetAppSetting", []any{key, value})
}

func (c *Client) UserSetting(ctx context.Context, key string) (v string, ok bool, err error) {
	err = c.call(ctx, "UserSetting", []any{key}, &v, &ok)
	return v, ok, err
}

func (c *Client) SetUserSetting(ctx context.Context, key, value string) error {
	return c.call(ctx, "SetUserSetting", []any{key, value})
}

// ---- 生成中の印 ---------------------------------------------------------------------------

func (c *Client) LockConversation(ctx context.Context, id uuid.UUID, holder string, until time.Time) error {
	return c.call(ctx, "LockConversation", []any{id, holder, until})
}

func (c *Client) UnlockConversation(ctx context.Context, id uuid.UUID, holder string) error {
	return c.call(ctx, "UnlockConversation", []any{id, holder})
}
