// shellquote_parse.go——npm shell-quote parse 的 Go 直译（parse.js
// 331 行全量语义）。上游包装壳为 eva-cli utils/bash/shellQuote.ts 的
// tryParseShellCommand（见 shellquote_wrap.go）。
//
// 语义要点（对齐 parse.js）：
//   - chunker 分块：控制操作符（||、&&、;;、|&、<(、<<<、>>、>&、<&、
//     单字符 [&;()|<>]）或 bareword/双引号/单引号的连续组合
//   - 引号扫描器：单引号内全字面；双引号内 \" \\ \$ 转义、$VAR 展开；
//     引号外反斜杠转义；引号上下文可在 token 中途切换（all'one'"x"）
//   - $VAR 展开（getVar）：env 未提供值且 key 非空 → 空串；key 空 →
//     '$'；object 值以哨兵对包裹 JSON（env 函数形态下还原为 object）
//   - 无引号 * / ? → glob token；# 注释截断后续
//   - env 函数形态：顶层对 string token 按哨兵对切分，JSON 还原
//     object token

package parser

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
)

// ParseTokenKind token 形态（对齐 ParseEntry 联合分支）。
type ParseTokenKind int

const (
	// TokenKindStr 字符串 token（含空串——引号空串 '' 是合法参数）
	TokenKindStr ParseTokenKind = iota
	// TokenKindOp 操作符 token（控制操作符或 "glob"）
	TokenKindOp
	// TokenKindComment 注释 token
	TokenKindComment
	// TokenKindObj env object 还原产物（JSON 文本承载）
	TokenKindObj
)

// ParseToken 单个 token（对齐 ParseEntry：string | {op} |
// {op:'glob',pattern} | {comment} | env object）。
type ParseToken struct {
	Kind    ParseTokenKind
	Str     string // KindStr：字符串内容
	Op      string // KindOp：操作符字面量（"glob" 或控制操作符）
	Pattern string // KindOp 且 Op=="glob"：glob 模式
	Comment string // KindComment：注释原文
	ObjJSON string // KindObj：object 的 JSON 文本
}

// IsOp string 形态便捷判定（对齐 isOperator——op 等值比较）。
func (t ParseToken) IsOp(op string) bool {
	return t.Kind == TokenKindOp && t.Op == op
}

// EnvFunc $VAR 展开取值函数（对齐 env map/function 双形态）。返回值
// string → 字面展开；其他非 nil → object 展开（JSON 哨兵对）；ok=false
// → undefined（按 getVar 语义回退）。
type EnvFunc func(key string) (value any, ok bool)

// envPreserveVar 上游标准 env 函数（`varName => \`$\${varName}\``）——
// 变量展开为字面 $VAR（保真不替换）。commands 管线统一使用。
func envPreserveVar(key string) (any, bool) {
	return "$" + key, true
}

// 正则族（与 parse.js 的 CONTROL/controlRE/SINGLE_QUOTE/DOUBLE_QUOTE/
// BAREWORD/chunker 同源——RE2 语义等价改写：& 与 > 无需转义）。
var (
	sqControl = `(?:\|\||&&|;;|\|&|<\(|<<<|>>|>&|<&|[&;()|<>])`
	sqControlRE = regexp.MustCompile(`^` + sqControl + `$`)
	sqSingleQuote = `'([^']*?)'`
	sqDoubleQuote = `"((\\"|[^"])*?)"`
	sqBareword = `(\\['"` + "|&;()<> \t" + `]|[^\s'"` + "|&;()<> \t" + `])+`
	sqChunker = regexp.MustCompile(
		`(` + sqControl + `)|((?:` + sqBareword + `|` + sqDoubleQuote + `|` + sqSingleQuote + `)+)`)
	sqHashRE = regexp.MustCompile(`^#$`)
	sqSpecialVarRE = regexp.MustCompile(`[*@#?$!_-]`)
	sqVarEndRE = regexp.MustCompile(`[^A-Za-z0-9_]`)
	sqDigitsOnlyRE = regexp.MustCompile(`^\d+$`)
)

// sqTokenSentinel object 展开的哨兵对（对齐 parse.js 的 TOKEN——
// 32 hex 随机串防碰撞；每进程一次）。
var sqTokenSentinel = func() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		// 随机源不可用——退化为固定哨兵（碰撞面可忽略，功能不中断）
		return "\x00SQTOKEN\x00"
	}
	return hex.EncodeToString(buf)
}()

var sqStartsWithToken = func() *regexp.Regexp {
	return regexp.MustCompile(`^` + sqTokenSentinel)
}()

// sqMatchAllChunks chunk 切分（对齐 matchAll——返回 [start,end) 字节
// 区间；parse.js 的空匹配推进逻辑不会触发：所有分支最短 1 字符）。
func sqMatchAllChunks(s string) [][2]int {
	locs := sqChunker.FindAllStringSubmatchIndex(s, -1)
	out := make([][2]int, 0, len(locs))
	for _, loc := range locs {
		out = append(out, [2]int{loc[0], loc[1]})
	}
	return out
}

// sqGetEnvVar getVar 直译：env 取值；undefined 且 key 非空 → pre+''；
// undefined 且 key 空 → pre+'$'；object → pre+哨兵+JSON+哨兵。
func sqGetEnvVar(env EnvFunc, pre, key string) string {
	if env == nil {
		// parse.js：!env 时 env = {}——任意 key 均 undefined
		if key != "" {
			return pre
		}
		return pre + "$"
	}
	v, ok := env(key)
	if !ok {
		if key != "" {
			return pre
		}
		return pre + "$"
	}
	if s, isStr := v.(string); isStr {
		return pre + s
	}
	b, err := json.Marshal(v)
	if err != nil {
		// 不可序列化 object——按空串展开（保守降级）
		return pre
	}
	return pre + sqTokenSentinel + string(b) + sqTokenSentinel
}

// sqParseInternal parseInternal 直译：chunk 切分 + 逐 chunk 扫描。
// chunkOffsetOf 返回 chunk 起点在原始命令中的偏移（comment 截取用）。
func sqParseInternal(s string, env EnvFunc) ([]ParseToken, int, error) {
	chunks := sqMatchAllChunks(s)
	if len(chunks) == 0 {
		return nil, 0, nil
	}

	commented := false
	tokens := make([]ParseToken, 0, len(chunks))

	for _, chunk := range chunks {
		if commented {
			continue
		}
		sub := s[chunk[0]:chunk[1]]
		if sub == "" {
			continue
		}
		if sqControlRE.MatchString(sub) {
			tokens = append(tokens, ParseToken{Kind: TokenKindOp, Op: sub})
			continue
		}
		tok, isComment, commentText, err := sqScanChunk(sub, s, chunk[0], env)
		if err != nil {
			return nil, 0, err
		}
		if isComment {
			commented = true
			tokens = append(tokens, ParseToken{Kind: TokenKindComment, Comment: commentText})
			continue
		}
		tokens = append(tokens, tok...)
	}
	return tokens, 0, nil
}

// sqScanChunk 单 chunk 引号扫描器（对齐 parse.js 122-295 的手写扫描
// 器：引号/转义/$VAR/glob/注释）。返回值：tokens / 是否命中注释 /
// 注释文本 / 错误（Bad substitution）。
func sqScanChunk(s, full string, chunkStart int, env EnvFunc) ([]ParseToken, bool, string, error) {
	runes := []rune(s)
	quote := byte(0) // 0 | '\'' | '"'
	esc := false
	var out strings.Builder
	isGlob := false

	parseEnvVar := func(i *int) (string, error) {
		*i++
		if *i >= len(runes) {
			return "", errors.New("bad substitution: unexpected end")
		}
		char := runes[*i]
		var varname string

		if char == '{' {
			*i++
			if *i < len(runes) && runes[*i] == '}' {
				return "", errors.New("bad substitution: ${}")
			}
			// 深度匹配花括号——嵌套 ${ 不会提前终止外层替换
			depth := 1
			varend := *i
			for depth > 0 && varend < len(runes) {
				if runes[varend] == '{' && varend > 0 && runes[varend-1] == '$' {
					depth++
				} else if runes[varend] == '}' {
					depth--
				}
				varend++
			}
			if depth != 0 {
				return "", errors.New("bad substitution: unclosed ${")
			}
			varend--
			varname = string(runes[*i : varend+1])
			*i = varend
		} else if sqSpecialVarRE.MatchString(string(char)) {
			varname = string(char)
		} else {
			rest := string(runes[*i:])
			loc := sqVarEndRE.FindStringIndex(rest)
			if loc == nil {
				varname = rest
				*i = len(runes)
			} else {
				varname = rest[:loc[0]]
				*i += loc[0] - 1
			}
		}
		return sqGetEnvVar(env, "", varname), nil
	}

	for i := 0; i < len(runes); i++ {
		c := runes[i]
		isGlob = isGlob || (quote == 0 && (c == '*' || c == '?'))
		if esc {
			out.WriteRune(c)
			esc = false
		} else if quote != 0 {
			if c == rune(quote) {
				quote = 0
			} else if quote == '\'' {
				out.WriteRune(c)
			} else { // 双引号内
				if c == '\\' {
					i++
					if i >= len(runes) {
						// JS charAt 越界返回 ''——out += '' 后结束
						break
					}
					c = runes[i]
					if c == '"' || c == '\\' || c == '$' {
						out.WriteRune(c)
					} else {
						out.WriteRune('\\')
						out.WriteRune(c)
					}
				} else if c == '$' {
					v, err := parseEnvVar(&i)
					if err != nil {
						return nil, false, "", err
					}
					out.WriteString(v)
				} else {
					out.WriteRune(c)
				}
			}
		} else if c == '"' || c == '\'' {
			quote = byte(c)
		} else if sqControlRE.MatchString(string(c)) {
			// 防御分支（chunker 保证 chunk 内不出现裸控制字符）
			return []ParseToken{{Kind: TokenKindOp, Op: s}}, false, "", nil
		} else if sqHashRE.MatchString(string(c)) {
			// 注释：原始命令从 chunk 起点 + 扫描位 + 1 起的剩余。
			// 按 rune 偏移换算 byte 偏移
			byteOff := len(string(runes[:i+1]))
			return nil, true, full[chunkStart+byteOff:], nil
		} else if c == '\\' {
			esc = true
		} else if c == '$' {
			v, err := parseEnvVar(&i)
			if err != nil {
				return nil, false, "", err
			}
			out.WriteString(v)
		} else {
			out.WriteRune(c)
		}
	}

	if isGlob {
		return []ParseToken{{Kind: TokenKindOp, Op: "glob", Pattern: out.String()}}, false, "", nil
	}
	// 无 ifs（splitUnquoted）路径：JS 恒 `return out`——每个 chunk 恒
	// 产出一个 string token（含空串；reduce 的 [].concat('') 展开）。
	// words/sawQuote 收尾机制为 ifs 专用分支，调用面未启用，不直译。
	return []ParseToken{{Kind: TokenKindStr, Str: out.String()}}, false, "", nil
}

// ShellQuoteParse shell-quote parse 入口（对齐顶层导出：env 函数形态下对
// string token 按哨兵对切分并 JSON 还原 object token）。
func ShellQuoteParse(s string, env EnvFunc) ([]ParseToken, error) {
	mapped, _, err := sqParseInternal(s, env)
	if err != nil {
		return nil, err
	}
	if env == nil {
		return mapped, nil
	}
	out := make([]ParseToken, 0, len(mapped))
	for _, t := range mapped {
		if t.Kind != TokenKindStr {
			out = append(out, t)
			continue
		}
		parts := sqSplitBySentinel(t.Str)
		if len(parts) == 1 {
			out = append(out, t)
			continue
		}
		for _, x := range parts {
			if x == "" {
				continue
			}
			if sqStartsWithToken.MatchString(x) {
				out = append(out, ParseToken{Kind: TokenKindObj,
					ObjJSON: x[len(sqTokenSentinel) : len(x)-len(sqTokenSentinel)]})
				continue
			}
			out = append(out, ParseToken{Kind: TokenKindStr, Str: x})
		}
	}
	return out, nil
}

// sqSplitBySentinel 按哨兵对切分（对齐 s.split(RegExp('(S.*?S)','g'))——
// 捕获组保留段形态：[前文, 哨兵对, 后文, 哨兵对, ...]，含空段——JS 中
// 哨兵串恰为整段时 split 仍返回三段（['', 对, '']），len==1 判定据此
// 区分"无哨兵"与"整段即哨兵对"）。
func sqSplitBySentinel(s string) []string {
	var out []string
	for {
		first := strings.Index(s, sqTokenSentinel)
		if first < 0 {
			out = append(out, s)
			return out
		}
		rest := s[first+len(sqTokenSentinel):]
		second := strings.Index(rest, sqTokenSentinel)
		if second < 0 {
			// 哨兵无配对——捕获正则不匹配，余量整体为普通文本段
			out = append(out, s)
			return out
		}
		pairEnd := first + len(sqTokenSentinel) + second + len(sqTokenSentinel)
		out = append(out, s[:first])
		out = append(out, s[first:pairEnd])
		s = s[pairEnd:]
	}
}

// sqDigitsOnly 纯数字判定（FD 前缀识别用，对齐 /^\d+$/）。
func sqDigitsOnly(s string) bool {
	return sqDigitsOnlyRE.MatchString(s)
}
