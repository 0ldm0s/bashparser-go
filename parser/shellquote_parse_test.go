// shellquote_parse_test.go——npm shell-quote parse 对拍测试（用例
// 转译自上游 node_modules/shell-quote/test/{parse,op,env_fn,comment}.js；
// splitUnquoted 与 escape 选项组为 eva-cli 调用面未用功能，不在直译
// 语义面内，不转译）。

package parser

import (
	"testing"
)

// tokStr 字符串 token 简写
func tokStr(s string) ParseToken { return ParseToken{Kind: TokenKindStr, Str: s} }

// tokOp 操作符 token 简写
func tokOp(op string) ParseToken { return ParseToken{Kind: TokenKindOp, Op: op} }

// tokGlob glob token 简写
func tokGlob(p string) ParseToken { return ParseToken{Kind: TokenKindOp, Op: "glob", Pattern: p} }

// tokComment 注释 token 简写
func tokComment(c string) ParseToken { return ParseToken{Kind: TokenKindComment, Comment: c} }

// tokObj object token 简写（JSON 文本承载）
func tokObj(jsonText string) ParseToken { return ParseToken{Kind: TokenKindObj, ObjJSON: jsonText} }

// tokensEqual token 切片比较（nil 与空切片等价——parse 空输入返回
// nil，上游期望形态为 []）
func tokensEqual(a, b []ParseToken) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestParseShellCommands parse shell commands 组（上游 test/parse.js 7-57）。
func TestParseShellCommands(t *testing.T) {
	cases := []struct {
		input string
		want  []ParseToken
	}{
		{"", []ParseToken{}},
		{"a 'b' \"c\"", []ParseToken{tokStr("a"), tokStr("b"), tokStr("c")}},
		{"beep \"boop\" 'foo bar baz' \"it's \\\"so\\\" groovy\"", []ParseToken{
			tokStr("beep"), tokStr("boop"), tokStr("foo bar baz"), tokStr("it's \"so\" groov" + "y"),
		}},
		{"a b\\ c d", []ParseToken{tokStr("a"), tokStr("b c"), tokStr("d")}},
		{"\\$beep bo\\`op", []ParseToken{tokStr("$beep"), tokStr("bo`op")}},
		{"echo \"foo = \\\"foo\\\"\"", []ParseToken{tokStr("echo"), tokStr("foo = \"foo\"")}},
		{" ", []ParseToken{}},
		{"\t", []ParseToken{}},
		{"a\"b c d\"e", []ParseToken{tokStr("ab c de")}},
		{"a\\ b\"c d\"\\ e f", []ParseToken{tokStr("a bc d e"), tokStr("f")}},
		{"x \"bl'a\"'h'", []ParseToken{tokStr("x"), tokStr("bl'ah")}},
		// 单引号内全字面——反斜杠不转义收尾引号
		{"'\\' '\\'", []ParseToken{tokStr("\\"), tokStr("\\")}},
		{"'\\'\\''", []ParseToken{tokStr("\\'")}},
		// 行首注释：内容不解析
		{"# abc  def  ghi", []ParseToken{tokComment(" abc  def  ghi")}},
		{"xyz # abc  def  ghi", []ParseToken{tokStr("xyz"), tokComment(" abc  def  ghi")}},
		// 空串保留
		{"-x \"\" -y", []ParseToken{tokStr("-x"), tokStr(""), tokStr("-y")}},
	}
	for _, tc := range cases {
		got, err := ShellQuoteParse(tc.input, nil)
		if err != nil {
			t.Errorf("Parse(%q) 意外报错：%v", tc.input, err)
			continue
		}
		if !tokensEqual(got, tc.want) {
			t.Errorf("Parse(%q)\n 得到 %v\n 期望 %v", tc.input, got, tc.want)
		}
	}
}

// TestParseThrows Bad substitution 抛错组（上游 parse.js 10-19）。
func TestParseThrows(t *testing.T) {
	for _, input := range []string{"${}", "${"} {
		if _, err := ShellQuoteParse(input, nil); err == nil {
			t.Errorf("Parse(%q) 应报 Bad substitution 错误", input)
		}
	}
}

// TestParseSingleQuotes 单引号字面组（上游 test/parse.js 59-69）。
func TestParseSingleQuotes(t *testing.T) {
	cases := []struct {
		input string
		want  []ParseToken
	}{
		{"'\\'\\''", []ParseToken{tokStr("\\'")}},
		{"'a'\\''b'", []ParseToken{tokStr("a'b")}},
		{"'\\'x", []ParseToken{tokStr("\\x")}},
		{"a'\\'b", []ParseToken{tokStr("a\\b")}},
		{"''", []ParseToken{tokStr("")}},
		{"''a''", []ParseToken{tokStr("a")}},
		{"'*'", []ParseToken{tokStr("*")}},
	}
	for _, tc := range cases {
		got, err := ShellQuoteParse(tc.input, nil)
		if err != nil {
			t.Errorf("Parse(%q) 意外报错：%v", tc.input, err)
			continue
		}
		if !tokensEqual(got, tc.want) {
			t.Errorf("Parse(%q)\n 得到 %v\n 期望 %v", tc.input, got, tc.want)
		}
	}
}

// TestParseUnmatchedSingleQuotes 未配对单引号宽松组（上游 parse.js 71-78）。
func TestParseUnmatchedSingleQuotes(t *testing.T) {
	cases := []struct {
		input string
		want  []ParseToken
	}{
		{"'", []ParseToken{}},
		{"'a", []ParseToken{tokStr("a")}},
		{"a'b", []ParseToken{tokStr("a"), tokStr("b")}},
	}
	for _, tc := range cases {
		got, err := ShellQuoteParse(tc.input, nil)
		if err != nil {
			t.Errorf("Parse(%q) 意外报错：%v", tc.input, err)
			continue
		}
		if !tokensEqual(got, tc.want) {
			t.Errorf("Parse(%q)\n 得到 %v\n 期望 %v", tc.input, got, tc.want)
		}
	}
}

// TestParseNestedExpansion 嵌套参数展开组（上游 parse.js 80-91——
// 无 env，getVar 回退空串）。
func TestParseNestedExpansion(t *testing.T) {
	cases := []struct {
		input string
		want  []ParseToken
	}{
		{"${a${b}c}", []ParseToken{tokStr("")}},
		{"${a${b}}", []ParseToken{tokStr("")}},
		{"${foo{bar}", []ParseToken{tokStr("")}},
		{"level=${levels[$RANDOM%${#levels[@]}]}", []ParseToken{tokStr("level=")}},
	}
	for _, tc := range cases {
		got, err := ShellQuoteParse(tc.input, nil)
		if err != nil {
			t.Errorf("Parse(%q) 意外报错：%v", tc.input, err)
			continue
		}
		if !tokensEqual(got, tc.want) {
			t.Errorf("Parse(%q)\n 得到 %v\n 期望 %v", tc.input, got, tc.want)
		}
	}
}

// TestParseSingleOperators 单操作符组（上游 test/op.js 6-27）。
func TestParseSingleOperators(t *testing.T) {
	cases := []struct {
		input string
		want  []ParseToken
	}{
		{"beep | boop", []ParseToken{tokStr("beep"), tokOp("|"), tokStr("boop")}},
		{"beep|boop", []ParseToken{tokStr("beep"), tokOp("|"), tokStr("boop")}},
		{"beep \\| boop", []ParseToken{tokStr("beep"), tokStr("|"), tokStr("boop")}},
		{"beep \"|boop\"", []ParseToken{tokStr("beep"), tokStr("|boop")}},
		{"echo zing &", []ParseToken{tokStr("echo"), tokStr("zing"), tokOp("&")}},
		{"echo zing&", []ParseToken{tokStr("echo"), tokStr("zing"), tokOp("&")}},
		{"echo zing\\&", []ParseToken{tokStr("echo"), tokStr("zing&")}},
		{"echo \"zing\\&\"", []ParseToken{tokStr("echo"), tokStr("zing\\&")}},
		{"beep;boop", []ParseToken{tokStr("beep"), tokOp(";"), tokStr("boop")}},
		{"(beep;boop)", []ParseToken{
			tokOp("("), tokStr("beep"), tokOp(";"), tokStr("boop"), tokOp(")"),
		}},
		{"beep>boop", []ParseToken{tokStr("beep"), tokOp(">"), tokStr("boop")}},
		{"beep 2>boop", []ParseToken{tokStr("beep"), tokStr("2"), tokOp(">"), tokStr("boop")}},
		{"beep<boop", []ParseToken{tokStr("beep"), tokOp("<"), tokStr("boop")}},
	}
	for _, tc := range cases {
		got, err := ShellQuoteParse(tc.input, nil)
		if err != nil {
			t.Errorf("Parse(%q) 意外报错：%v", tc.input, err)
			continue
		}
		if !tokensEqual(got, tc.want) {
			t.Errorf("Parse(%q)\n 得到 %v\n 期望 %v", tc.input, got, tc.want)
		}
	}
}

// TestParseDoubleOperators 双操作符组（上游 test/op.js 29-70）。
func TestParseDoubleOperators(t *testing.T) {
	cases := []struct {
		input string
		want  []ParseToken
	}{
		{"beep || boop", []ParseToken{tokStr("beep"), tokOp("||"), tokStr("boop")}},
		{"beep||boop", []ParseToken{tokStr("beep"), tokOp("||"), tokStr("boop")}},
		{"beep ||boop", []ParseToken{tokStr("beep"), tokOp("||"), tokStr("boop")}},
		{"beep  ||   boop", []ParseToken{tokStr("beep"), tokOp("||"), tokStr("boop")}},
		{"beep && boop || byte", []ParseToken{
			tokStr("beep"), tokOp("&&"), tokStr("boop"), tokOp("||"), tokStr("byte"),
		}},
		{"beep&&boop||byte", []ParseToken{
			tokStr("beep"), tokOp("&&"), tokStr("boop"), tokOp("||"), tokStr("byte"),
		}},
		{"beep\\&\\&boop||byte", []ParseToken{
			tokStr("beep&&boop"), tokOp("||"), tokStr("byte"),
		}},
		{"beep\\&&boop||byte", []ParseToken{
			tokStr("beep&"), tokOp("&"), tokStr("boop"), tokOp("||"), tokStr("byte"),
		}},
		{"beep;;boop|&byte>>blip", []ParseToken{
			tokStr("beep"), tokOp(";;"), tokStr("boop"), tokOp("|&"),
			tokStr("byte"), tokOp(">>"), tokStr("blip"),
		}},
		{"beep 2>&1", []ParseToken{tokStr("beep"), tokStr("2"), tokOp(">&"), tokStr("1")}},
		{"beep<(boop)", []ParseToken{
			tokStr("beep"), tokOp("<("), tokStr("boop"), tokOp(")"),
		}},
		{"beep<<(boop)", []ParseToken{
			tokStr("beep"), tokOp("<"), tokOp("<("), tokStr("boop"), tokOp(")"),
		}},
	}
	for _, tc := range cases {
		got, err := ShellQuoteParse(tc.input, nil)
		if err != nil {
			t.Errorf("Parse(%q) 意外报错：%v", tc.input, err)
			continue
		}
		if !tokensEqual(got, tc.want) {
			t.Errorf("Parse(%q)\n 得到 %v\n 期望 %v", tc.input, got, tc.want)
		}
	}
}

// TestParseFdDuplication FD 复制组（上游 test/op.js 72-83）。
func TestParseFdDuplication(t *testing.T) {
	cases := []struct {
		input string
		want  []ParseToken
	}{
		{"beep 3<&1", []ParseToken{tokStr("beep"), tokStr("3"), tokOp("<&"), tokStr("1")}},
		{"beep <&1", []ParseToken{tokStr("beep"), tokOp("<&"), tokStr("1")}},
		{"beep <&-", []ParseToken{tokStr("beep"), tokOp("<&"), tokStr("-")}},
	}
	for _, tc := range cases {
		got, err := ShellQuoteParse(tc.input, nil)
		if err != nil {
			t.Errorf("Parse(%q) 意外报错：%v", tc.input, err)
			continue
		}
		if !tokensEqual(got, tc.want) {
			t.Errorf("Parse(%q)\n 得到 %v\n 期望 %v", tc.input, got, tc.want)
		}
	}
}

// TestParseHereStrings here-string 组（上游 test/op.js 85-92）。
func TestParseHereStrings(t *testing.T) {
	cases := []struct {
		input string
		want  []ParseToken
	}{
		{"cat <<< \"hello world\"", []ParseToken{tokStr("cat"), tokOp("<<<"), tokStr("hello world")}},
		{"cat <<< hello", []ParseToken{tokStr("cat"), tokOp("<<<"), tokStr("hello")}},
		{"cat<<<hello", []ParseToken{tokStr("cat"), tokOp("<<<"), tokStr("hello")}},
		{"cat<<<\"hello world\"", []ParseToken{tokStr("cat"), tokOp("<<<"), tokStr("hello world")}},
	}
	for _, tc := range cases {
		got, err := ShellQuoteParse(tc.input, nil)
		if err != nil {
			t.Errorf("Parse(%q) 意外报错：%v", tc.input, err)
			continue
		}
		if !tokensEqual(got, tc.want) {
			t.Errorf("Parse(%q)\n 得到 %v\n 期望 %v", tc.input, got, tc.want)
		}
	}
}

// TestParseGlobPatterns glob 组（上游 test/op.js 94-102）。
func TestParseGlobPatterns(t *testing.T) {
	cases := []struct {
		input string
		want  []ParseToken
	}{
		{"tap test/*.test.js", []ParseToken{tokStr("tap"), tokGlob("test/*.test.js")}},
		{"tap \"test/*.test.js\"", []ParseToken{tokStr("tap"), tokStr("test/*.test.js")}},
	}
	for _, tc := range cases {
		got, err := ShellQuoteParse(tc.input, nil)
		if err != nil {
			t.Errorf("Parse(%q) 意外报错：%v", tc.input, err)
			continue
		}
		if !tokensEqual(got, tc.want) {
			t.Errorf("Parse(%q)\n 得到 %v\n 期望 %v", tc.input, got, tc.want)
		}
	}
}

// TestParseEnvFunc env 函数展开组（上游 test/env_fn.js 14-21）。
func TestParseEnvFunc(t *testing.T) {
	getEnv := func(key string) (any, bool) { return "xxx", true }
	got, err := ShellQuoteParse("a $XYZ c", getEnv)
	if err != nil {
		t.Fatalf("意外报错：%v", err)
	}
	want := []ParseToken{tokStr("a"), tokStr("xxx"), tokStr("c")}
	if !tokensEqual(got, want) {
		t.Errorf("字符串 env 展开\n 得到 %v\n 期望 %v", got, want)
	}

	getEnvObj := func(key string) (any, bool) {
		return map[string]any{"op": "@@"}, true
	}
	got, err = ShellQuoteParse("a $XYZ c", getEnvObj)
	if err != nil {
		t.Fatalf("意外报错：%v", err)
	}
	want = []ParseToken{tokStr("a"), tokObj(`{"op":"@@"}`), tokStr("c")}
	if !tokensEqual(got, want) {
		t.Errorf("object env 展开\n 得到 %v\n 期望 %v", got, want)
	}

	// 引号内的 object 展开（上游 20 行：parse('"a $XYZ c"', getEnvObj)）
	got, err = ShellQuoteParse("\"a $XYZ c\"", getEnvObj)
	if err != nil {
		t.Fatalf("意外报错：%v", err)
	}
	want = []ParseToken{tokStr("a "), tokObj(`{"op":"@@"}`), tokStr(" c")}
	if !tokensEqual(got, want) {
		t.Errorf("双引号内 object 展开\n 得到 %v\n 期望 %v", got, want)
	}
}
