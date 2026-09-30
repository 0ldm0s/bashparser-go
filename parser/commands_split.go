// commands_split.go——eva-cli utils/bash/commands.ts 拆分族的 Go 直译：
//   - generatePlaceholders（20-36）：防注入随机盐占位符
//   - splitCommandWithOperators（85-249）：shell 操作符拆分
//     （heredoc 预提取 → 行连续合并 → 占位符预处理 → shell-quote
//     解析 → 相邻串折叠 → token 映射 → 占位符还原 → heredoc 还原）
//   - splitCommand_DEPRECATED（265-369）：legacy 拆分 + 重定向剥离
//   - filterControlOperators（251-257）
//   - isStaticRedirectTarget（47-81）
//   - isCommandList（539-603）
//   - isUnsafeCompoundCommand_DEPRECATED（609-624）
//   - COMMAND_LIST_SEPARATORS / ALL_SUPPORTED_CONTROL_OPERATORS
//
// @deprecated 标记族：上游仅 tree-sitter 不可用时使用（主门为
// parseForSecurity）——Go 侧供 checkPathConstraints 非 AST 回退分支
// 与 legacy 消费点使用。

package parser

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
)

// sqPlaceholders 防注入占位符五元组（对齐 generatePlaceholders——
// 8 字节随机盐防命令文本字面含占位符的参数注入）。
type sqPlaceholders struct {
	singleQuote    string
	doubleQuote    string
	newLine        string
	escOpenParen   string
	escCloseParen  string
}

func sqGeneratePlaceholders() sqPlaceholders {
	buf := make([]byte, 8)
	salt := "0000000000000000"
	if _, err := rand.Read(buf); err == nil {
		salt = hex.EncodeToString(buf)
	}
	return sqPlaceholders{
		singleQuote:   "__SINGLE_QUOTE_" + salt + "__",
		doubleQuote:   "__DOUBLE_QUOTE_" + salt + "__",
		newLine:       "__NEW_LINE_" + salt + "__",
		escOpenParen:  "__ESCAPED_OPEN_PAREN_" + salt + "__",
		escCloseParen: "__ESCAPED_CLOSE_PAREN_" + salt + "__",
	}
}

// sqAllowedFileDescriptors 标准流文件描述符集（对齐
// ALLOWED_FILE_DESCRIPTORS——0/1/2）
var sqAllowedFileDescriptors = map[string]bool{"0": true, "1": true, "2": true}

// CommandListSeparators 命令列表分隔符集（对齐 COMMAND_LIST_SEPARATORS）
var CommandListSeparators = map[string]bool{
	"&&": true, "||": true, ";": true, ";;": true, "|": true,
}

// AllSupportedControlOperators 全部受支持控制操作符（对齐
// ALL_SUPPORTED_CONTROL_OPERATORS）
var AllSupportedControlOperators = func() map[string]bool {
	m := map[string]bool{}
	for k := range CommandListSeparators {
		m[k] = true
	}
	m[">&"] = true
	m[">"] = true
	m[">>"] = true
	return m
}()

// SplitCommandWithOperators 按 shell 操作符拆分命令（对齐
// splitCommandWithOperators 85-249 全序）。
func SplitCommandWithOperators(command string) []string {
	// heredoc 预提取——shell-quote 对 << 解析错乱
	heredocRes := ExtractHeredocs(command, false)
	ph := sqGeneratePlaceholders()

	// 行连续合并（heredoc 提取后、解析前）：奇数反斜杠+换行 = 连续
	// （偶数为转义序列配对，换行是分隔符不合并——安全语义见上游
	// 96-120：不得补空格、须奇偶判定）
	joinContinuations := func(s string) string {
		var b strings.Builder
		for i := 0; i < len(s); {
			if s[i] == '\\' {
				// 数连续反斜杠串
				j := i
				for j < len(s) && s[j] == '\\' {
					j++
				}
				run := j - i
				if j < len(s) && s[j] == '\n' {
					if run%2 == 1 {
						// 奇数：最后一个反斜杠转义换行——剥之，保留其余
						b.WriteString(strings.Repeat(`\`, run-1))
						i = j + 1
						continue
					}
					// 偶数：全为字面转义序列，换行保留
					b.WriteString(strings.Repeat(`\`, run))
					i = j
					continue
				}
				b.WriteString(strings.Repeat(`\`, run))
				i = j
				continue
			}
			b.WriteByte(s[i])
			i++
		}
		return b.String()
	}
	joined := joinContinuations(heredocRes.ProcessedCommand)
	// 原始命令（heredoc 提取前）的连续合并——解析失败回退路径专用
	// （回退须返回单元素：换行已合并才与 bash 实际执行一致）
	commandOriginalJoined := joinContinuations(command)

	// 占位符预处理（对齐 142-148——parse() 会剥引号/吞换行/转义括号）
	preprocessed := strings.ReplaceAll(joined, `"`, `"`+ph.doubleQuote)
	preprocessed = strings.ReplaceAll(preprocessed, `'`, `'`+ph.singleQuote)
	preprocessed = strings.ReplaceAll(preprocessed, "\n", "\n"+ph.newLine+"\n")
	preprocessed = strings.ReplaceAll(preprocessed, `\(`, ph.escOpenParen)
	preprocessed = strings.ReplaceAll(preprocessed, `\)`, ph.escCloseParen)

	parseRes := TryParseShellCommand(preprocessed, envPreserveVar)
	if !parseRes.Success {
		// 畸形语法整体视为单命令（返回连续合并后的原始串——安全语义
		// 见上游 122-132）
		return []string{commandOriginalJoined}
	}
	parsed := parseRes.Tokens
	if len(parsed) == 0 {
		return []string{}
	}

	// 相邻串折叠（对齐 172-191）：string+string 合并（NEW_LINE 分段）；
	// glob 并入前导 string
	type partOrBreak struct {
		token ParseToken
		brk   bool
	}
	var parts []partOrBreak
	for _, part := range parsed {
		if part.Kind == TokenKindStr {
			if len(parts) > 0 && !parts[len(parts)-1].brk &&
				parts[len(parts)-1].token.Kind == TokenKindStr {
				if part.Str == ph.newLine {
					parts = append(parts, partOrBreak{brk: true})
				} else {
					parts[len(parts)-1].token.Str += " " + part.Str
				}
				continue
			}
		} else if part.Kind == TokenKindOp && part.Op == "glob" {
			if len(parts) > 0 && !parts[len(parts)-1].brk &&
				parts[len(parts)-1].token.Kind == TokenKindStr {
				parts[len(parts)-1].token.Str += " " + part.Pattern
				continue
			}
		}
		parts = append(parts, partOrBreak{token: part})
	}

	// token → string 映射（对齐 194-229）
	var stringParts []string
	for _, p := range parts {
		if p.brk {
			stringParts = append(stringParts, "")
			continue
		}
		tok := p.token
		switch tok.Kind {
		case TokenKindStr:
			stringParts = append(stringParts, tok.Str)
		case TokenKindComment:
			// 注释原样保留注入的引号前缀标记——剥之防递归调用时占位
			// 符指数膨胀（ReDoS，上游 202-220）
			cleaned := strings.ReplaceAll(tok.Comment, `"`+ph.doubleQuote, ph.doubleQuote)
			cleaned = strings.ReplaceAll(cleaned, `'`+ph.singleQuote, ph.singleQuote)
			stringParts = append(stringParts, "#"+cleaned)
		case TokenKindOp:
			if tok.Op == "glob" {
				stringParts = append(stringParts, tok.Pattern)
			} else {
				stringParts = append(stringParts, tok.Op)
			}
		default:
			// env object 形态——commands 管线 env 恒 preserveVar，不可达
			continue
		}
	}

	// 占位符还原（对齐 232-239）
	var quotedParts []string
	for _, part := range stringParts {
		s := strings.ReplaceAll(part, ph.singleQuote, `'`)
		s = strings.ReplaceAll(s, ph.doubleQuote, `"`)
		s = strings.ReplaceAll(s, "\n"+ph.newLine+"\n", "\n")
		s = strings.ReplaceAll(s, ph.escOpenParen, `\(`)
		s = strings.ReplaceAll(s, ph.escCloseParen, `\)`)
		quotedParts = append(quotedParts, s)
	}

	return RestoreHeredocs(quotedParts, heredocRes.Heredocs)
}

// FilterControlOperators 过滤纯控制操作符段（对齐 filterControlOperators）
func FilterControlOperators(commandsAndOperators []string) []string {
	var out []string
	for _, part := range commandsAndOperators {
		if AllSupportedControlOperators[part] {
			continue
		}
		out = append(out, part)
	}
	return out
}

// IsStaticRedirectTarget 静态重定向目标判定（对齐 isStaticRedirectTarget
// 47-81：单一 shell 词、无任何动态展开形态——含空格/引号即拒（相邻串
// 折叠合并形态）、# 前缀拒（shell-quote 注释差分加固））。
func IsStaticRedirectTarget(target string) bool {
	if target == "" {
		return false
	}
	if strings.ContainsAny(target, " \t\n'\"") {
		return false
	}
	if strings.HasPrefix(target, "#") {
		return false
	}
	dynamic := []string{"!", "=", "$", "`", "*", "?", "[", "{", "~", "(", "<", "&"}
	for _, ch := range dynamic {
		if ch == "!" || ch == "=" || ch == "&" {
			if strings.HasPrefix(target, ch) {
				return false
			}
			continue
		}
		if strings.Contains(target, ch) {
			return false
		}
	}
	return true
}

// SplitCommandDeprecated legacy 拆分 + 重定向剥离（对齐
// splitCommand_DEPRECATED 265-369）：标准流重定向（2>&1、>file、
// >>file）剥离以免权限提示中出现碎片命令；文件目标安全校验由
// checkPathConstraints 独立进行。
func SplitCommandDeprecated(command string) []string {
	parts := SplitCommandWithOperators(command)
	for i := 0; i < len(parts); i++ {
		part := parts[i]

		if part != ">&" && part != ">" && part != ">>" {
			continue
		}
		prevPart := ""
		if i > 0 {
			prevPart = strings.TrimSpace(parts[i-1])
		}
		nextPart := ""
		nextExists := i+1 < len(parts)
		if nextExists {
			nextPart = strings.TrimSpace(parts[i+1])
		}
		afterNextPart := ""
		if i+2 < len(parts) {
			afterNextPart = strings.TrimSpace(parts[i+2])
		}
		if !nextExists {
			continue
		}

		shouldStrip := false
		stripThirdToken := false

		// 相邻串折叠把 `/dev/null 2` 合并为一段（> /dev/null 2>&1
		// 形态）——尾部 ` <FD>` 是下一重定向的 FD 前缀，拆出
		effectiveNextPart := nextPart
		if (part == ">" || part == ">>") && len(nextPart) >= 3 &&
			nextPart[len(nextPart)-2] == ' ' &&
			sqAllowedFileDescriptors[string(nextPart[len(nextPart)-1])] &&
			(afterNextPart == ">" || afterNextPart == ">>" || afterNextPart == ">&") {
			effectiveNextPart = nextPart[:len(nextPart)-2]
		}

		if part == ">&" && sqAllowedFileDescriptors[nextPart] {
			// 2>&1 紧凑形态
			shouldStrip = true
		} else if part == ">" && nextPart == "&" &&
			afterNextPart != "" && sqAllowedFileDescriptors[afterNextPart] {
			// 2 > &1 全空格形态
			shouldStrip = true
			stripThirdToken = true
		} else if part == ">" && strings.HasPrefix(nextPart, "&") &&
			len(nextPart) > 1 && sqAllowedFileDescriptors[nextPart[1:]] {
			// 2 > &1 半空格形态
			shouldStrip = true
		} else if (part == ">" || part == ">>") &&
			IsStaticRedirectTarget(effectiveNextPart) {
			// 一般文件重定向：仅剥静态目标；动态形态（$、`、* 等）
			// 保留可见
			shouldStrip = true
		}

		if shouldStrip {
			// 剥前段尾随 FD（`echo foo 2` → `echo foo`）——仅当前导
			// 字符为空格且剥离后非空（shell-quote 区分不了 `2>` 与
			// `2 >`，安全语义见上游 346-352）
			if prevPart != "" && len(prevPart) >= 3 &&
				sqAllowedFileDescriptors[string(prevPart[len(prevPart)-1])] &&
				prevPart[len(prevPart)-2] == ' ' {
				parts[i-1] = prevPart[:len(prevPart)-2]
			}
			parts[i] = ""
			if i+1 < len(parts) {
				parts[i+1] = ""
			}
			if stripThirdToken && i+2 < len(parts) {
				parts[i+2] = ""
			}
		}
	}
	var stringParts []string
	for _, p := range parts {
		if p != "" {
			stringParts = append(stringParts, p)
		}
	}
	return FilterControlOperators(stringParts)
}

// IsCommandList 是否纯命令列表（对齐 isCommandList 539-603：仅含
// string/glob/命令列表分隔符/受控重定向 token——无危险操作符）。
func IsCommandList(command string) bool {
	ph := sqGeneratePlaceholders()
	heredocRes := ExtractHeredocs(command, false)
	preprocessed := strings.ReplaceAll(heredocRes.ProcessedCommand, `"`, `"`+ph.doubleQuote)
	preprocessed = strings.ReplaceAll(preprocessed, `'`, `'`+ph.singleQuote)
	parseRes := TryParseShellCommand(preprocessed, envPreserveVar)
	if !parseRes.Success {
		return false
	}
	parts := parseRes.Tokens
	for i := 0; i < len(parts); i++ {
		part := parts[i]
		switch part.Kind {
		case TokenKindStr:
			continue
		case TokenKindComment:
			return false // 注释不可信——可含命令注入
		case TokenKindOp:
			if part.Op == "glob" {
				continue
			}
			if CommandListSeparators[part.Op] {
				continue
			}
			if part.Op == ">&" {
				// 标准流 FD 重定向安全（下一 token 须为 0/1/2）
				if i+1 < len(parts) && parts[i+1].Kind == TokenKindStr &&
					sqAllowedFileDescriptors[strings.TrimSpace(parts[i+1].Str)] {
					continue
				}
				return false
			}
			if part.Op == ">" || part.Op == ">>" {
				// 输出重定向由 pathValidation 校验
				continue
			}
			return false
		default:
			return false
		}
	}
	return true
}

// IsUnsafeCompoundCommandDeprecated legacy 复合命令危险判定（对齐
// isUnsafeCompoundCommand_DEPRECATED 609-624）：解析失败恒不安全
// （纵深防御）；拆分出多命令且非纯命令列表 = 不安全。
func IsUnsafeCompoundCommandDeprecated(command string) bool {
	heredocRes := ExtractHeredocs(command, false)
	parseRes := TryParseShellCommand(heredocRes.ProcessedCommand, envPreserveVar)
	if !parseRes.Success {
		return true
	}
	return len(SplitCommandDeprecated(command)) > 1 && !IsCommandList(command)
}
