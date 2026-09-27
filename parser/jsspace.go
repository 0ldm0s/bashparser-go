// jsspace.go——JS 引擎 \s 语义设施。
//
// 上游（eva-cli utils/bash 各模块）的正则跑在 JS 引擎上，\s 为
// Unicode 全集空白：[\t\n\v\f\r ] + U+00A0/1680/2000-200A/2028/
// 2029/202F/205F/3000/FEFF。Go RE2 的 \s 仅 [\t\n\f\r ]——上游正则
// 的 Go 直译凡遇 \s，以本包常量拼接字符类对齐（差异全集恰为：
// JS 多 \v 与 U+FEFF 等集合成员、少 U+0085；两侧逐码点见 IsJSSpace）。
//
// 本文件非上游直译物，是对拍基础设施：凡 1:1 移植上游 JS 正则的
// 消费方（bashperm/shell/tools 等）统一从本包取用，禁止本地复制。
package parser

import "strings"

// JSSpaceInner JS \s 全集的 RE2 字符类内芯（无外括号），供正则
// 字面量拼接：`\d` + "[" + JSSpaceInner + "]*" 形态。
const JSSpaceInner = `\t\n\v\f\r \x{00A0}\x{1680}\x{2000}-\x{200A}\x{2028}\x{2029}\x{202F}\x{205F}\x{3000}\x{FEFF}`

// JSSpace JS \s 全集字符类（带外括号）。
const JSSpace = "[" + JSSpaceInner + "]"

// JSSpaceNon JS \S 全集字符类（\s 取补）。
const JSSpaceNon = "[^" + JSSpaceInner + "]"

// IsJSSpace 判定 r 是否属于 JS \s 字符集（TrimFunc/FieldsFunc 用的
// rune 版）。与 Go unicode.IsSpace 的差异：JS 含 U+FEFF 不含 U+0085，
// Go 相反。
func IsJSSpace(r rune) bool {
	switch {
	case r <= 0x7F:
		return r == ' ' || r == '\t' || r == '\n' || r == '\v' || r == '\f' || r == '\r'
	case r == 0xA0, r == 0x1680,
		r >= 0x2000 && r <= 0x200A,
		r == 0x2028, r == 0x2029, r == 0x202F, r == 0x205F,
		r == 0x3000, r == 0xFEFF:
		return true
	}
	return false
}

// TrimJSSpace 剥除 s 首尾的 JS \s 空白（等价 JS string.trim()）。
func TrimJSSpace(s string) string {
	return strings.TrimFunc(s, IsJSSpace)
}
