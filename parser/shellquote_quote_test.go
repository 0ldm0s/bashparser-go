// shellquote_quote_test.go——npm shell-quote quote 对拍测试（用例
// 转译自上游 node_modules/shell-quote/test/quote.js；windows paths
// 组上游已 skip，不转译；非字符串参数组由 Go 强类型承载，不转译）。

package parser

import (
	"strings"
	"testing"
)

// TestQuoteStrings 字符串参数组（上游 quote.js 7-30）。
func TestQuoteStrings(t *testing.T) {
	cases := []struct {
		args []QuoteArg
		want string
	}{
		{[]QuoteArg{{QuoteStr, "a", "", "", ""}, {QuoteStr, "b", "", "", ""}, {QuoteStr, "c d", "", "", ""}}, `a b 'c d'`},
		{[]QuoteArg{{QuoteStr, "a", "", "", ""}, {QuoteStr, "b", "", "", ""}, {QuoteStr, "it's a \"neat thing\"", "", "", ""}}, `a b "it's a \"neat thing\""`},
		{[]QuoteArg{{QuoteStr, "$", "", "", ""}, {QuoteStr, "`", "", "", ""}, {QuoteStr, "'", "", "", ""}}, "\\$ \\" + "`" + " \"'\""},
		{[]QuoteArg{{QuoteStr, "a\nb", "", "", ""}}, "'a\nb'"},
		{[]QuoteArg{{QuoteStr, " #(){}*|][!", "", "", ""}}, `' #(){}*|][!'`},
		{[]QuoteArg{{QuoteStr, "'#(){}*|][!", "", "", ""}}, "\"'#(){}*|][\\!\""},
		{[]QuoteArg{{QuoteStr, "X#(){}*|][!", "", "", ""}}, `X\#\(\)\{\}\*\|\]\[\!`},
		{[]QuoteArg{{QuoteStr, "a\n#\nb", "", "", ""}}, "'a\n#\nb'"},
		{[]QuoteArg{{QuoteStr, "><;{}", "", "", ""}}, `\>\<\;\{\}`},
		{[]QuoteArg{{QuoteStr, "a\\x", "", "", ""}}, `'a\x'`},
		{[]QuoteArg{{QuoteStr, "a\"b", "", "", ""}}, `'a"b'`},
		{[]QuoteArg{{QuoteStr, "\"a\"b\"", "", "", ""}}, `'"a"b"'`},
		{[]QuoteArg{{QuoteStr, "a\\\"b", "", "", ""}}, `'a\"b'`},
		{[]QuoteArg{{QuoteStr, "a\\b", "", "", ""}}, `'a\b'`},
		{[]QuoteArg{{QuoteStr, "-x", "", "", ""}, {QuoteStr, "", "", "", ""}, {QuoteStr, "y", "", "", ""}}, `-x '' y`},
		{[]QuoteArg{{QuoteStr, "`:\\a\\b", "", "", ""}}, "'`:\\a\\b'"},
	}
	for _, tc := range cases {
		got, err := ShellQuoteQuote(tc.args)
		if err != nil {
			t.Errorf("Quote(%v) 意外报错：%v", tc.args, err)
			continue
		}
		if got != tc.want {
			t.Errorf("Quote 得到 %q 期望 %q", got, tc.want)
		}
	}
}

// TestQuoteTilde tilde 全转义组（上游 quote.js 33-43）。
func TestQuoteTilde(t *testing.T) {
	cases := []struct{ in, want string }{
		{"~", `\~`},
		{"~/foo", `\~/foo`},
		{"~root", `\~root`},
		{"~root/x", `\~root/x`},
		{"~+", `\~+`},
		{"~-", `\~-`},
		{"a~b", `a\~b`},
		{"x~", `x\~`},
	}
	for _, tc := range cases {
		got, err := ShellQuoteQuote([]QuoteArg{{Kind: QuoteStr, Str: tc.in}})
		if err != nil {
			t.Errorf("Quote(%q) 意外报错：%v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("Quote(%q) 得到 %q 期望 %q", tc.in, got, tc.want)
		}
	}
}

// TestQuoteBackslashWhitespace 反斜杠与空白组（上游 quote.js 45-50）。
func TestQuoteBackslashWhitespace(t *testing.T) {
	cases := []struct{ in, want string }{
		{"foo \\ bar", `'foo \ bar'`},
		// 上游 JS 字面 'foo \\\\ bar' = 两反斜杠；输出单引号内保留
		{strings.Join([]string{"foo", "\\\\", "bar"}, " "), "'foo \\\\ bar'"},
	}
	for _, tc := range cases {
		got, err := ShellQuoteQuote([]QuoteArg{{Kind: QuoteStr, Str: tc.in}})
		if err != nil {
			t.Errorf("Quote(%q) 意外报错：%v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("Quote(%q) 得到 %q 期望 %q", tc.in, got, tc.want)
		}
	}
}

// TestQuoteShellSpecial 保守 shell 特殊字符转义组（上游 quote.js 52-60）。
func TestQuoteShellSpecial(t *testing.T) {
	cases := []struct{ in, want string }{
		{"CFLAGS=-DRELEASE", `CFLAGS\=-DRELEASE`},
		{"a@b", `a\@b`},
		{"a^b", `a\^b`},
		{"a:b", `a\:b`},
		{"a,b", `a\,b`},
		{"a!b", `a\!b`},
	}
	for _, tc := range cases {
		got, err := ShellQuoteQuote([]QuoteArg{{Kind: QuoteStr, Str: tc.in}})
		if err != nil {
			t.Errorf("Quote(%q) 意外报错：%v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("Quote(%q) 得到 %q 期望 %q", tc.in, got, tc.want)
		}
	}
}

// TestQuoteOps 操作符组（上游 quote.js 62-69 + 91-100 allowlist 全表）。
func TestQuoteOps(t *testing.T) {
	got, err := ShellQuoteQuote([]QuoteArg{
		{Kind: QuoteStr, Str: "a"}, {Kind: QuoteOp, Op: "|"}, {Kind: QuoteStr, Str: "b"},
	})
	if err != nil || got != `a \| b` {
		t.Errorf("op | 得到 %q（err=%v）", got, err)
	}

	ops := []string{"||", "&&", ";;", "|&", "<(", "<<<", ">>", ">&", "<&", "&", ";", "(", ")", "|", "<", ">"}
	for _, op := range ops {
		var want strings.Builder
		for _, ch := range op {
			want.WriteByte('\\')
			want.WriteRune(ch)
		}
		got, err := ShellQuoteQuote([]QuoteArg{{Kind: QuoteOp, Op: op}})
		if err != nil {
			t.Errorf("op %q 意外报错：%v", op, err)
			continue
		}
		if got != want.String() {
			t.Errorf("op %q 得到 %q 期望 %q", op, got, want.String())
		}
	}
}

// TestQuoteOpsRejects 操作符拒收组（上游 quote.js 102-115）。
func TestQuoteOpsRejects(t *testing.T) {
	for _, op := range []string{";\nid", ";\rid", ";\u2028id", ";\u2029id", "", "foo", "|||"} {
		if _, err := ShellQuoteQuote([]QuoteArg{{Kind: QuoteOp, Op: op}}); err == nil {
			t.Errorf("op %q 应拒收", op)
		}
	}
}

// TestQuoteGlob glob 组（上游 quote.js 118-128）。
func TestQuoteGlob(t *testing.T) {
	cases := []struct{ in, want string }{
		{"test/*.test.js", "test/*.test.js"},
		{"?ab", "?ab"},
		{"[ab]c", "[ab]c"},
		{"{a,b}", "{a,b}"},
		{"my dir/*.txt", `my\ dir/*.txt`},
		{"a$b", `a\$b`},
	}
	for _, tc := range cases {
		got, err := ShellQuoteQuote([]QuoteArg{{Kind: QuoteGlob, Pattern: tc.in}})
		if err != nil {
			t.Errorf("glob %q 意外报错：%v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("glob %q 得到 %q 期望 %q", tc.in, got, tc.want)
		}
	}
	for _, bad := range []string{"a\nb", "a\u2028b"} {
		if _, err := ShellQuoteQuote([]QuoteArg{{Kind: QuoteGlob, Pattern: bad}}); err == nil {
			t.Errorf("glob %q 应拒收（含换行）", bad)
		}
	}
}

// TestQuoteComment 注释组（上游 quote.js 131-138）。
func TestQuoteComment(t *testing.T) {
	got, err := ShellQuoteQuote([]QuoteArg{
		{Kind: QuoteStr, Str: "echo"}, {Kind: QuoteStr, Str: "hi"},
		{Kind: QuoteComment, Comment: " a comment"},
	})
	if err != nil || got != "echo hi # a comment" {
		t.Errorf("comment 得到 %q（err=%v）", got, err)
	}

	got, err = ShellQuoteQuote([]QuoteArg{{Kind: QuoteComment, Comment: ""}})
	if err != nil || got != "#" {
		t.Errorf("空 comment 得到 %q（err=%v）", got, err)
	}

	for _, bad := range []string{"a\nb", "a\rb", "a\u2028b"} {
		if _, err := ShellQuoteQuote([]QuoteArg{{Kind: QuoteComment, Comment: bad}}); err == nil {
			t.Errorf("comment %q 应拒收（含换行）", bad)
		}
	}
}

// TestQuoteUnknownObj 未识别 object 拒收（上游 quote.js 140-145）。
func TestQuoteUnknownObj(t *testing.T) {
	if _, err := ShellQuoteQuote([]QuoteArg{{Kind: QuoteUnknownObj}}); err == nil {
		t.Errorf("未识别 object 应拒收")
	}
}
