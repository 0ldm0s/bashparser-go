package parser

import "unicode/utf8"

// lexer.go：词法状态与字符判定（对齐 bashParser.ts 105-271 行）。
//
// 上游以 UTF-16 code unit 计游标 i（代理对 +2 格 +4 字节）；Go 以
// code point 计 i（非 BMP 字符 1 个 rune / 4 字节）——字节偏移与文本
// 输出语义等价，rune 索引值可能不同但不出现在任何输出中。

// lexer 词法状态
type Lexer struct {
	runes    []rune
	len      int
	i        int // rune 索引
	b        int // UTF-8 字节偏移
	heredocs []heredocPending
	// byteTable rune 索引 → 字节偏移（懒构建，对齐上游 byteTable）
	byteTable []int
}

// heredocPending 待扫描体的 heredoc 定界符（对齐 HeredocPending）
type heredocPending struct {
	delim     string
	stripTabs bool
	quoted    bool
	bodyStart int
	bodyEnd   int
	endStart  int
	endEnd    int
}

func makeLexer(src string) *Lexer {
	return &Lexer{runes: []rune(src), len: len([]rune(src))}
}

// MakeLexer 构造词法状态（对齐 makeLexer）
func MakeLexer(src string) *Lexer {
	return &Lexer{runes: []rune(src), len: len([]rune(src))}
}

// advance 前进一个字符并更新字节偏移（对齐 advance）
func advance(L *Lexer) {
	r := L.runes[L.i]
	L.i++
	L.b += utf8.RuneLen(r)
}

// peek 取偏移 off 处的字符（越界返回 0；上游返回空串）
func peek(L *Lexer, off int) rune {
	if L.i+off < L.len {
		return L.runes[L.i+off]
	}
	return 0
}

// byteAtChar rune 索引处的字节偏移（懒构建偏移表，对齐 byteAt）
func byteAtChar(L *Lexer, runeIdx int) int {
	if L.byteTable != nil {
		return L.byteTable[runeIdx]
	}
	t := make([]int, L.len+1)
	b := 0
	for i := 0; i < L.len; i++ {
		t[i] = b
		b += utf8.RuneLen(L.runes[i])
	}
	t[L.len] = b
	L.byteTable = t
	return t[runeIdx]
}

// sliceRunes 上游 L.src.slice(si, ei) 等价
func sliceRunes(L *Lexer, si, ei int) string {
	return string(L.runes[si:ei])
}

// isWordChar bash 词字符（对齐 isWordChar：字母数字 + 不起操作符的标点）
func isWordChar(c rune) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9') ||
		c == '_' || c == '/' || c == '.' || c == '-' || c == '+' ||
		c == ':' || c == '@' || c == '%' || c == ',' || c == '~' ||
		c == '^' || c == '?' || c == '*' || c == '!' || c == '=' ||
		c == '[' || c == ']'
}

func isWordStart(c rune) bool { return isWordChar(c) || c == '\\' }

func isIdentStart(c rune) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_'
}

func isIdentChar(c rune) bool { return isIdentStart(c) || (c >= '0' && c <= '9') }

func isDigit(c rune) bool { return c >= '0' && c <= '9' }

func isHexDigit(c rune) bool {
	return isDigit(c) || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

// isBaseDigit bash BASE#DIGITS：字母数字下划线与 @（至多 64 进制）
func isBaseDigit(c rune) bool { return isIdentChar(c) || c == '@' }

// isHeredocDelimChar 未加引号 heredoc 定界符字符：bash 接受大多数非
// 元字符（不止标识符）——停在空白/重定向/管道/列表操作符/结构符；
// 允许 !、-、.、+ 等（如 <<!HEREDOC!）
func isHeredocDelimChar(c rune) bool {
	if c == 0 {
		return false
	}
	switch c {
	case ' ', '\t', '\n', '<', '>', '|', '&', ';', '(', ')', '\'', '"', '`', '\\':
		return false
	}
	return true
}
