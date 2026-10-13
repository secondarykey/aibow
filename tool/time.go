package tool

// time.go: 現在時刻のツール(get_current_time)。
//
// モデルは学習した時点までのことしか知らず、「今日」がいつかを知らない。
// 時刻をツールで渡すと、知らないことを「調べて」答える、というツールの効果が分かりやすい。

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	// 配布したアプリでも Asia/Tokyo などの名前でタイムゾーンを引けるよう、タイムゾーンの表を埋め込む(約 450KB)。
	// Windows にはこの表が無く、Go のインストールに付いているものを使うので、Go の無い PC では引けなくなる
	_ "time/tzdata"
)

type currentTime struct {
	now func() time.Time // テストで時刻を決めるため。nil なら time.Now
}

func (currentTime) Def() Def {
	return Def{
		Name:        "get_current_time",
		Description: "Get the current date, day of the week, time and time zone.",
		Parameters: json.RawMessage(`{"type": "object", "properties": {"timezone": {"type": "string", ` +
			`"description": "IANA time zone name such as Asia/Tokyo or America/New_York. Omit to use the user's local time zone."}}, "required": []}`),
	}
}

func (c currentTime) Run(ctx context.Context, args Args) (string, error) {
	now := time.Now
	if c.now != nil {
		now = c.now
	}
	loc, zone := time.Local, "local time"
	if tz := args.String("timezone"); tz != "" {
		l, err := time.LoadLocation(tz)
		if err != nil {
			return "", fmt.Errorf("タイムゾーン %q が見つかりません。Asia/Tokyo のような IANA の名前で指定するか、省略してください", tz)
		}
		loc, zone = l, tz
	}
	t := now().In(loc)
	// 例: 2026-10-13 15:04:05 Tuesday (Asia/Tokyo, UTC+09:00)
	return fmt.Sprintf("%s %s (%s, UTC%s)", t.Format(time.DateTime), t.Weekday(), zone, t.Format("-07:00")), nil
}
