package tool

// calc.go: 計算のツール(calculate)。
//
// LLM は計算が苦手(数字もトークンの並びとして「それらしく」続けているだけ)。4B のモデルは
// 4桁 × 4桁の掛け算をよく間違える。計算をツールに任せると、ツールの効果が分かりやすい。
//
// 計算機は自作せず、Go の標準ライブラリを使う:
//
//	go/parser   で式を Go の式として読む(括弧や演算子の優先順位は Go の文法のまま)
//	go/types    の types.Eval で、定数式として計算する(有理数で正確に計算される)
//
// Go の規則では整数どうしの割り算は切り捨て(7/2 が 3)なので、整数の数を小数の数(7.0)に書き換えてから計算する。
// 定数式は小数も有理数のまま扱うので、0.1+0.2 も 0.3 になる(float64 の誤差が出ない)。

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/constant"
	"go/parser"
	"go/printer"
	"go/token"
	"go/types"
	"math"
	"strconv"
	"strings"
)

type calculator struct{}

func (calculator) Def() Def {
	return Def{
		Name:        "calculate",
		Description: "Evaluate an arithmetic expression exactly. Supports numbers, + - * / and parentheses.",
		Parameters: json.RawMessage(`{"type": "object", "properties": {"expression": {"type": "string", ` +
			`"description": "Arithmetic expression such as (1234 + 5) * 5678 / 7"}}, "required": ["expression"]}`),
	}
}

func (calculator) Run(ctx context.Context, args map[string]string) (string, error) {
	return calculate(args["expression"])
}

// errCalcSyntax は使える書き方の説明。式が読めないときのエラーに添える。
var errCalcSyntax = errors.New("使えるのは数・+ - * /・括弧だけです(× ÷ ^ % や関数は使えません)")

// calculate は四則演算と括弧の式を計算する。
func calculate(expr string) (string, error) {
	if strings.TrimSpace(expr) == "" {
		return "", fmt.Errorf("式が空です。%w", errCalcSyntax)
	}
	e, err := parser.ParseExpr(expr)
	if err != nil {
		return "", fmt.Errorf("式を読めません(%v)。%w", err, errCalcSyntax)
	}
	if err := toFloatExpr(e); err != nil {
		return "", err
	}

	// 書き換えた式を文字列に戻して、定数式として計算する
	var src bytes.Buffer
	if err := printer.Fprint(&src, token.NewFileSet(), e); err != nil {
		return "", err
	}
	tv, err := types.Eval(token.NewFileSet(), nil, token.NoPos, src.String())
	if err != nil {
		if strings.Contains(err.Error(), "division by zero") {
			return "", errors.New("0 で割っています")
		}
		return "", fmt.Errorf("計算できません(%v)", err)
	}
	return formatNumber(tv.Value), nil
}

// toFloatExpr は、式が数・+ - * /・括弧だけでできているかを確かめ、整数の数を小数の数に書き換える(7 → 7.0)。
// 関数呼び出しや変数などは受け付けない(types.Eval は Go の式なら何でも評価してしまうため)。
func toFloatExpr(e ast.Expr) error {
	var bad error
	ast.Inspect(e, func(n ast.Node) bool {
		if bad != nil || n == nil {
			return false
		}
		switch x := n.(type) {
		case *ast.BasicLit:
			switch x.Kind {
			case token.INT:
				// 0x10 などの 10 進数以外の書き方は、".0" を足すと別の意味になるので受け付けない
				if len(x.Value) > 1 && strings.ContainsAny(x.Value[:2], "xXoObB") {
					bad = fmt.Errorf("数は 10 進数で書いてください(%s)", x.Value)
					return false
				}
				x.Kind, x.Value = token.FLOAT, x.Value+".0"
			case token.FLOAT:
			default:
				bad = fmt.Errorf("数ではないものがあります(%s)。%w", x.Value, errCalcSyntax)
			}
		case *ast.BinaryExpr:
			switch x.Op {
			case token.ADD, token.SUB, token.MUL, token.QUO:
			default:
				bad = fmt.Errorf("演算子 %s は使えません。%w", x.Op, errCalcSyntax)
			}
		case *ast.UnaryExpr:
			if x.Op != token.ADD && x.Op != token.SUB {
				bad = fmt.Errorf("演算子 %s は使えません。%w", x.Op, errCalcSyntax)
			}
		case *ast.ParenExpr:
		default:
			bad = errCalcSyntax
		}
		return bad == nil
	})
	return bad
}

// formatNumber は計算結果を文字列にする。整数ならそのまま全桁(7006652)、
// そうでなければ float64 で表せる一番短い形(3.5、0.3333333333333333)。
func formatNumber(v constant.Value) string {
	if i := constant.ToInt(v); i.Kind() == constant.Int {
		return i.ExactString()
	}
	f, _ := constant.Float64Val(v)
	if a := math.Abs(f); a >= 1e-6 && a < 1e21 {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}
