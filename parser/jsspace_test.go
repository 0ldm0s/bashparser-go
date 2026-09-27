package parser

import (
	"regexp"
	"testing"
)

// TestIsJSSpace 逐码点边界：与 Go unicode.IsSpace 的双向差异
// （JS 含 \v 与 U+FEFF、不含 U+0085）是 1:1 对拍的语义关键。
func TestIsJSSpace(t *testing.T) {
	cases := []struct {
		r    rune
		want bool
	}{
		{' ', true}, {'\t', true}, {'\n', true},
		{'\v', true}, // RE2 \s 不含，JS \s 含——关键差异成员
		{'\f', true}, {'\r', true},
		{0x85, false}, // NEL：unicode.IsSpace 含，JS \s 不含——关键差异成员
		{0xA0, true}, {0x1680, true}, {0x2000, true}, {0x200A, true},
		{0x200B, false}, // 200A 之后的近邻码点
		{0x2028, true}, {0x2029, true}, {0x202F, true}, {0x205F, true},
		{0x3000, true}, // 全角空格
		{0xFEFF, true}, // BOM：JS \s 含——关键差异成员
		{'a', false}, {'中', false}, {0x10FFFF, false}, {-1, false},
	}
	for _, c := range cases {
		if got := IsJSSpace(c.r); got != c.want {
			t.Errorf("IsJSSpace(%U) = %v, 期望 %v", c.r, got, c.want)
		}
	}
}

// TestJSSpaceRegexCompile 字符类拼接可编译且行为对齐 JS 语义
// （\v 分隔的位移形态上游 JS 正则可命中——RE2 \s 则不可）。
func TestJSSpaceRegexCompile(t *testing.T) {
	re := regexp.MustCompile(`\d[` + JSSpaceInner + `]*<<[` + JSSpaceInner + `]*\d`)
	if !re.MatchString("1\v<<\v2") {
		t.Error("\\v 分隔的位移形态应命中（JS \\s 含 \\v）")
	}
	if !re.MatchString("1　<<　2") {
		t.Error("全角空格分隔的位移形态应命中")
	}
	if re.MatchString("1a<<2") {
		t.Error("非空白分隔不应命中")
	}
	if got := TrimJSSpace("\uFEFF hi\u00A0"); got != "hi" {
		t.Errorf("TrimJSSpace = %q, 期望 %q", got, "hi")
	}
}
