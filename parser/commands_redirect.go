// commands_redirect.go——eva-cli utils/bash/commands.ts 634-1340
// extractOutputRedirections 族的 Go 直译：从命令串提取输出重定向
// （shell-quote 路径）。08 档裁定此族不移植进 eva-go（AST 替代）；
// 2026-09-30 拍板由本库承载（checkPathConstraints 非 AST 回退分支
// 兜底——第三消费点）。
//
// 安全语义（上游安全注释要点，直译保留）：
//   - heredoc 先于行连续合并提取（顺序错误会让 > /etc/passwd 藏进
//     heredoc 体——上游 642-667 攻击样例）
//   - 解析失败 fail-closed（hasDangerousRedirection=true——静默跳过
//     即绕过，上游 688-698）
//   - isSimpleTarget 与 hasDangerousExpansion 构成完备二分：任一
//     string 目标要么被捕获校验要么被标记危险——不落双空
//   - zsh 强覆盖（>! / >| / 2>! / >>&! 族）目标剥离 ! / | 后校验
//   - 进程替换、命令替换内的重定向排除（cmdSubDepth）

package parser

import (
	"regexp"
	"strings"
)

// 重定向提取族正则
var (
	// sqBangDigit 历史展开 !数字 形态（对齐 /^!\d/）
	sqBangDigit = regexp.MustCompile(`^!\d`)
	// sqFdRedirectShape FD 重定向形态（对齐 /^\d+>>?$/——2>、2>>、1>）
	sqFdRedirectShape = regexp.MustCompile(`^\d+>>?$`)
	// sqJSSpaceNonRe JS \S 全集正则（needsQuoting 空白判定——jsspace
	// 常量编译件）
	sqJSSpaceNonRe = regexp.MustCompile(JSSpaceNon)
	// sqJSSpaceRe JS \s 全集正则（含空白判定）
	sqJSSpaceRe = regexp.MustCompile(JSSpace)
)

// OutputRedirection 输出重定向形态复用 parsed_command.go 既有定义
// （Target + Operator——同构，不重复声明）。

// RedirectionsExtraction 提取结果（对齐 extractOutputRedirections
// 返回形态）。
type RedirectionsExtraction struct {
	CommandWithoutRedirections string
	Redirections               []OutputRedirection
	HasDangerousRedirection    bool
}

// ExtractOutputRedirections 提取输出重定向（对齐 extractOutputRedirections
// 634-790 主流程）。
func ExtractOutputRedirections(cmd string) RedirectionsExtraction {
	var redirections []OutputRedirection
	hasDangerousRedirection := false

	// 安全：heredoc 先于行连续合并与解析（顺序即安全——上游 642-667
	// 引号 heredoc 体内容为字面文本，\ 不转义、合并会让 > 目标被吞）
	heredocRes := ExtractHeredocs(cmd, false)

	// 行连续合并：奇数反斜杠+换行剥转义反斜杠（偶数为转义对，换行
	// 是分隔符——上游 670-683）
	processedCommand := joinBackslashNewline(heredocRes.ProcessedCommand)

	parseRes := TryParseShellCommand(processedCommand, envPreserveVar)

	// 安全：解析失败 fail-closed——无法确认存在哪些重定向，命令中
	// 任何 > 都可能写文件（对齐上游 688-698）
	if !parseRes.Success {
		return RedirectionsExtraction{
			CommandWithoutRedirections: cmd,
			Redirections:               nil,
			HasDangerousRedirection:    true,
		}
	}
	parsed := parseRes.Tokens

	// 识别被重定向的子 shell（"(cmd) > file" 形态——上游 703-727）
	redirectedSubshells := map[int]bool{}
	type parenFrame struct {
		index   int
		isStart bool
	}
	var parenStack []parenFrame
	for i, part := range parsed {
		if part.IsOp("(") {
			isStart := i == 0 ||
				(i > 0 && (parsed[i-1].IsOp("&&") || parsed[i-1].IsOp("||") ||
					parsed[i-1].IsOp(";") || parsed[i-1].IsOp("|")))
			parenStack = append(parenStack, parenFrame{i, isStart})
		} else if part.IsOp(")") && len(parenStack) > 0 {
			opening := parenStack[len(parenStack)-1]
			parenStack = parenStack[:len(parenStack)-1]
			next := ParseToken{}
			if i+1 < len(parsed) {
				next = parsed[i+1]
			}
			if opening.isStart && (next.IsOp(">") || next.IsOp(">>")) {
				redirectedSubshells[opening.index] = true
				redirectedSubshells[i] = true
			}
		}
	}

	// 主循环：提取重定向（命令替换深度外）——上游 729-780
	var kept []ParseToken
	cmdSubDepth := 0
	for i := 0; i < len(parsed); i++ {
		part := parsed[i]
		prev := ParseToken{}
		if i > 0 {
			prev = parsed[i-1]
		}
		next := ParseToken{}
		if i+1 < len(parsed) {
			next = parsed[i+1]
		}
		nextNext := ParseToken{}
		if i+2 < len(parsed) {
			nextNext = parsed[i+2]
		}
		nextNextNext := ParseToken{}
		if i+3 < len(parsed) {
			nextNextNext = parsed[i+3]
		}

		// 跳过被重定向子 shell 的括号
		if (part.IsOp("(") || part.IsOp(")")) && redirectedSubshells[i] {
			continue
		}

		// 命令替换深度追踪（$( 的 ( ）
		if part.IsOp("(") && prev.Kind == TokenKindStr && strings.HasSuffix(prev.Str, "$") {
			cmdSubDepth++
		} else if part.IsOp(")") && cmdSubDepth > 0 {
			cmdSubDepth--
		}

		// 命令替换外提取重定向
		if cmdSubDepth == 0 {
			skip, dangerous := sqHandleRedirection(
				part, prev, next, nextNext, nextNextNext,
				&redirections, &kept)
			if dangerous {
				hasDangerousRedirection = true
			}
			if skip > 0 {
				i += skip
				continue
			}
		}

		kept = append(kept, part)
	}

	return RedirectionsExtraction{
		CommandWithoutRedirections: RestoreHeredocsInString(
			sqReconstructCommand(kept, processedCommand), heredocRes.Heredocs),
		Redirections:            redirections,
		HasDangerousRedirection: hasDangerousRedirection,
	}
}

// joinBackslashNewline 行连续合并（对齐 /\\+\n/ 替换——奇数反斜杠剥
// 转义反斜杠与换行，偶数保留；见 splitCommand 族同款实现）
func joinBackslashNewline(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == '\\' {
			j := i
			for j < len(s) && s[j] == '\\' {
				j++
			}
			run := j - i
			if j < len(s) && s[j] == '\n' {
				if run%2 == 1 {
					b.WriteString(strings.Repeat(`\`, run-1))
					i = j + 1
					continue
				}
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

// sqIsSimpleTarget 简单目标判定（对齐 isSimpleTarget 798-817）：非空
// string 且无历史展开/zsh 等号/tilde/变量/反引号/glob/花括号。设计
// 不变量：string 目标要么命中本判定（→捕获校验）要么命中
// hasDangerousExpansion（→标记危险）——双不命中即校验漏网。
func sqIsSimpleTarget(target ParseToken) bool {
	if target.Kind != TokenKindStr || target.Str == "" {
		return false
	}
	s := target.Str
	return !strings.HasPrefix(s, "!") && // 历史展开 !!/!-1/!foo
		!strings.HasPrefix(s, "=") && // zsh 等号展开（=cmd → 路径）
		!strings.HasPrefix(s, "~") && // tilde 展开
		!strings.Contains(s, "$") && // 变量/命令替换
		!strings.Contains(s, "`") && // 反引号替换
		!strings.Contains(s, "*") && // glob 通配
		!strings.Contains(s, "?") && // glob 单字符
		!strings.Contains(s, "[") && // glob 字符类
		!strings.Contains(s, "{") // 花括号展开 {a,b}/{1..5}
}

// sqHasDangerousExpansion 危险展开判定（对齐 hasDangerousExpansion
// 830-858）：glob token 恒危险（运行时展开）；string 含 $ % 反引号
// * ? [ { 字符或 ! = ~ 前缀则危险。全部 tilde 前缀均危险（~ 与
// ~/path 曾被 carve out 造成大于 ~/.bashrc 双不命中间隙——
// bug_007/bug_022）。
func sqHasDangerousExpansion(target ParseToken) bool {
	if target.Kind == TokenKindOp {
		return target.Op == "glob"
	}
	if target.Kind != TokenKindStr {
		return false
	}
	s := target.Str
	if s == "" {
		return false
	}
	return strings.ContainsAny(s, "$%`*?[{") ||
		strings.HasPrefix(s, "!") ||
		strings.HasPrefix(s, "=") ||
		strings.HasPrefix(s, "~")
}

// sqIsFileDescriptor 纯数字判定（FD 前缀——trim 后全数字）
func sqIsFileDescriptor(p ParseToken) bool {
	return p.Kind == TokenKindStr && sqDigitsOnly(strings.TrimSpace(p.Str))
}

// sqHandleRedirection 单 token 重定向处理（对齐 handleRedirection
// 860-1091）。返回 (skip, dangerous)。
func sqHandleRedirection(
	part, prev, next, nextNext, nextNextNext ParseToken,
	redirections *[]OutputRedirection,
	kept *[]ParseToken,
) (int, bool) {

	// > 与 >> 操作符族（上游 872-1049）
	if part.IsOp(">") || part.IsOp(">>") {
		operator := part.Op

		// FD 重定向形态（2>、3> 等）
		if sqIsFileDescriptor(prev) {
			// zsh 强覆盖 2>! file / 2>>! file
			if next.Kind == TokenKindStr && next.Str == "!" && sqIsSimpleTarget(nextNext) {
				return sqHandleFileDescriptorRedirection(
					strings.TrimSpace(prev.Str), operator, nextNext,
					redirections, kept, 2)
			}
			if next.Kind == TokenKindStr && next.Str == "!" && sqHasDangerousExpansion(nextNext) {
				return 0, true
			}
			// POSIX 强覆盖 2>| file / 2>>| file
			if next.IsOp("|") && sqIsSimpleTarget(nextNext) {
				return sqHandleFileDescriptorRedirection(
					strings.TrimSpace(prev.Str), operator, nextNext,
					redirections, kept, 2)
			}
			if next.IsOp("|") && sqHasDangerousExpansion(nextNext) {
				return 0, true
			}
			// 2>!filename（无空格）——zsh 展开剩余部分；排除历史展开
			// 形态（!!、!-n、!?、!数字）
			if next.Kind == TokenKindStr && strings.HasPrefix(next.Str, "!") &&
				len(next.Str) > 1 &&
				next.Str[1] != '!' && next.Str[1] != '-' && next.Str[1] != '?' &&
				!sqBangDigit.MatchString(next.Str) {
				afterBang := next.Str[1:]
				// 安全：校验 zsh 解读形态（剥 ! 后）
				if sqHasDangerousExpansion(ParseToken{Kind: TokenKindStr, Str: afterBang}) {
					return 0, true
				}
				return sqHandleFileDescriptorRedirection(
					strings.TrimSpace(prev.Str), operator,
					ParseToken{Kind: TokenKindStr, Str: afterBang},
					redirections, kept, 1)
			}
			return sqHandleFileDescriptorRedirection(
				strings.TrimSpace(prev.Str), operator, next,
				redirections, kept, 1)
		}

		// >| 强覆盖（> 后跟 |）
		if next.IsOp("|") && sqIsSimpleTarget(nextNext) {
			*redirections = append(*redirections,
				OutputRedirection{Target: nextNext.Str, Operator: operator})
			return 2, false
		}
		if next.IsOp("|") && sqHasDangerousExpansion(nextNext) {
			return 0, true
		}

		// >! zsh 强覆盖（> 后跟 "!"）
		if next.Kind == TokenKindStr && next.Str == "!" && sqIsSimpleTarget(nextNext) {
			*redirections = append(*redirections,
				OutputRedirection{Target: nextNext.Str, Operator: operator})
			return 2, false
		}
		if next.Kind == TokenKindStr && next.Str == "!" && sqHasDangerousExpansion(nextNext) {
			return 0, true
		}

		// >!filename（无空格）——! 成为文件名一部分捕获校验；排除
		// 历史展开形态
		if next.Kind == TokenKindStr && strings.HasPrefix(next.Str, "!") &&
			len(next.Str) > 1 &&
			next.Str[1] != '!' && next.Str[1] != '-' && next.Str[1] != '?' &&
			!sqBangDigit.MatchString(next.Str) {
			afterBang := next.Str[1:]
			if sqHasDangerousExpansion(ParseToken{Kind: TokenKindStr, Str: afterBang}) {
				return 0, true
			}
			// 安全：推 afterBang（剥 !）非 next——zsh 强覆盖时目标是
			// filename 非 !filename；推 !filename 会被 path.resolve 当
			// 相对路径绕过绝对路径校验（上游 993-1002）
			*redirections = append(*redirections,
				OutputRedirection{Target: afterBang, Operator: operator})
			return 1, false
		}

		// >>&! 与 >>&|——stdout/stderr 合并强覆盖形态
		if next.IsOp("&") {
			if nextNext.Kind == TokenKindStr && nextNext.Str == "!" && sqIsSimpleTarget(nextNextNext) {
				*redirections = append(*redirections,
					OutputRedirection{Target: nextNextNext.Str, Operator: operator})
				return 3, false
			}
			if nextNext.Kind == TokenKindStr && nextNext.Str == "!" && sqHasDangerousExpansion(nextNextNext) {
				return 0, true
			}
			if nextNext.IsOp("|") && sqIsSimpleTarget(nextNextNext) {
				*redirections = append(*redirections,
					OutputRedirection{Target: nextNextNext.Str, Operator: operator})
				return 3, false
			}
			if nextNext.IsOp("|") && sqHasDangerousExpansion(nextNextNext) {
				return 0, true
			}
			// >>& 普通合并不带强修饰
			if sqIsSimpleTarget(nextNext) {
				*redirections = append(*redirections,
					OutputRedirection{Target: nextNextNext.Str, Operator: operator})
				return 2, false
			}
			if sqHasDangerousExpansion(nextNext) {
				return 0, true
			}
		}

		// 标准 stdout 重定向
		if sqIsSimpleTarget(next) {
			*redirections = append(*redirections,
				OutputRedirection{Target: next.Str, Operator: operator})
			return 1, false
		}

		// 目标含危险展开（> $VAR / > %VAR%）
		if sqHasDangerousExpansion(next) {
			return 0, true
		}
	}

	// >& 操作符族（上游 1051-1088）
	if part.IsOp(">&") {
		// FD 复制（2>&1）——原样保留（重建时处理）
		if sqIsFileDescriptor(prev) && sqIsFileDescriptor(next) {
			return 0, false
		}

		// >&| POSIX 合并强覆盖
		if next.IsOp("|") && sqIsSimpleTarget(nextNext) {
			*redirections = append(*redirections,
				OutputRedirection{Target: nextNext.Str, Operator: ">"})
			return 2, false
		}
		if next.IsOp("|") && sqHasDangerousExpansion(nextNext) {
			return 0, true
		}

		// >&! zsh 合并强覆盖
		if next.Kind == TokenKindStr && next.Str == "!" && sqIsSimpleTarget(nextNext) {
			*redirections = append(*redirections,
				OutputRedirection{Target: nextNext.Str, Operator: ">"})
			return 2, false
		}
		if next.Kind == TokenKindStr && next.Str == "!" && sqHasDangerousExpansion(nextNext) {
			return 0, true
		}

		// stdout+stderr 合并写文件
		if sqIsSimpleTarget(next) && !sqIsFileDescriptor(next) {
			*redirections = append(*redirections,
				OutputRedirection{Target: next.Str, Operator: ">"})
			return 1, false
		}

		// 目标危险展开（>& $VAR）
		if !sqIsFileDescriptor(next) && sqHasDangerousExpansion(next) {
			return 0, true
		}
	}

	return 0, false
}

// sqHandleFileDescriptorRedirection FD 重定向处理（对齐
// handleFileDescriptorRedirection 1093-1140）：stdout 的重定向剥离出
// kept；非 stdout 保留在命令中。
func sqHandleFileDescriptorRedirection(
	fd string,
	operator string,
	target ParseToken,
	redirections *[]OutputRedirection,
	kept *[]ParseToken,
	skipCount int,
) (int, bool) {
	isStdout := fd == "1"
	isFileTarget := target.Kind == TokenKindStr &&
		sqIsSimpleTarget(target) && !sqDigitsOnly(strings.TrimSpace(target.Str))
	isFdTarget := target.Kind == TokenKindStr && sqDigitsOnly(strings.TrimSpace(target.Str))

	// 恒从 kept 移除 FD 数字（上游 1109-1110）
	if len(*kept) > 0 {
		*kept = (*kept)[:len(*kept)-1]
	}

	// 安全：任何早退前先查危险展开（2>$HOME/file 形态——上游
	// 1112-1116）
	if !isFdTarget && sqHasDangerousExpansion(target) {
		return 0, true
	}

	// 文件目标（2>/tmp/file 形态）
	if isFileTarget {
		*redirections = append(*redirections,
			OutputRedirection{Target: target.Str, Operator: operator})
		// 非 stdout：重定向保留在命令中
		if !isStdout {
			*kept = append(*kept,
				ParseToken{Kind: TokenKindStr, Str: fd + operator},
				ParseToken{Kind: TokenKindStr, Str: target.Str})
		}
		return skipCount, false
	}

	// FD 到 FD 复制（2>&1）——仅非 stdout 保留
	if !isStdout {
		*kept = append(*kept, ParseToken{Kind: TokenKindStr, Str: fd + operator})
		if target.Kind == TokenKindStr {
			*kept = append(*kept, target)
			return 1, false
		}
	}

	return 0, false
}

// sqDetectCommandSubstitution 命令替换检测（对齐 detectCommandSubstitution
// 1143-1168）：独立 $ 前缀、赋值形态（x=$）、或闭括号后紧跟非空格。
func sqDetectCommandSubstitution(prev ParseToken, kept []ParseToken, index int) bool {
	if prev.Kind != TokenKindStr {
		return false
	}
	if prev.Str == "$" {
		return true
	}
	if strings.HasSuffix(prev.Str, "$") {
		// 赋值形态（result=$）
		if strings.Contains(prev.Str, "=") && strings.HasSuffix(prev.Str, "=$") {
			return true
		}
		// 闭括号后紧跟非空格文本
		depth := 1
		for j := index + 1; j < len(kept) && depth > 0; j++ {
			if kept[j].IsOp("(") {
				depth++
			}
			if kept[j].IsOp(")") {
				depth--
				if depth == 0 {
					after := ParseToken{}
					if j+1 < len(kept) {
						after = kept[j+1]
					}
					return after.Kind == TokenKindStr && !strings.HasPrefix(after.Str, " ")
				}
			}
		}
	}
	return false
}

// sqNeedsQuoting 重建引号需求判定（对齐 needsQuoting 1171-1187）：FD
// 重定向形态不引；含任意空白（JS \s 全集语义）须引；单字符 shell 操
// 作符须引。
func sqNeedsQuoting(s string) bool {
	// FD 重定向形态（2>、2>>、1> 等）
	if sqFdRedirectShape.MatchString(s) {
		return false
	}
	// JS \s 全集（空格/tab/换行/CR/VT/FF/NBSP 等空白——含空白即须引；
	// ENV_VAR_PATTERN 消费方用 \s+，漏检会让 stripSafeWrappers 跨空白
	// 匹配——上游 1175-1181 安全注释）
	if sqJSSpaceRe.MatchString(s) {
		return true
	}
	// 单字符 shell 操作符
	if len([]rune(s)) == 1 && strings.ContainsRune("><|&;()", []rune(s)[0]) {
		return true
	}
	return false
}

// sqAddToken 附加 token（空格分隔；noSpace 时不加——对齐 addToken）
func sqAddToken(result, token string, noSpace bool) string {
	if result == "" || noSpace {
		return result + token
	}
	return result + " " + token
}

// sqReconstructCommand 从 kept token 重建无重定向命令（对齐
// reconstructCommand 1195-1339）。result 以 string 赋值语义推进
//（JS `result = addToken(result, ...)` 直译——非累加）。
func sqReconstructCommand(kept []ParseToken, originalCmd string) string {
	if len(kept) == 0 {
		return originalCmd
	}

	result := ""
	cmdSubDepth := 0
	inProcessSub := false

	for i := 0; i < len(kept); i++ {
		part := kept[i]
		prev := ParseToken{}
		if i > 0 {
			prev = kept[i-1]
		}
		next := ParseToken{}
		if i+1 < len(kept) {
			next = kept[i+1]
		}

		// string 分支（上游 1208-1241）
		if part.Kind == TokenKindStr {
			hasCommandSeparator := strings.ContainsAny(part.Str, "|&;")
			str := part.Str
			if hasCommandSeparator {
				str = `"` + part.Str + `"`
			} else if sqNeedsQuoting(part.Str) {
				str, _ = ShellQuoteQuote([]QuoteArg{{Kind: QuoteStr, Str: part.Str}})
			}

			// <( 后加空格特例；否则按 noSpace 规则拼接
			if strings.HasSuffix(result, "<(") {
				result += " " + str
			} else {
				noSpace := strings.HasSuffix(result, "(") ||
					(prev.Kind == TokenKindStr && prev.Str == "$") ||
					(prev.Kind == TokenKindOp && prev.Op == ")")
				result = sqAddToken(result, str, noSpace)
			}
			continue
		}

		if part.Kind != TokenKindOp {
			continue
		}
		op := part.Op

		// glob：pattern 原样拼接
		if op == "glob" {
			result = sqAddToken(result, part.Pattern, false)
			continue
		}

		// FD 复制（2>&1）——回退合并前段数字与操作符
		if op == ">&" && prev.Kind == TokenKindStr && sqDigitsOnly(prev.Str) &&
			next.Kind == TokenKindStr && sqDigitsOnly(next.Str) {
			lastIdx := strings.LastIndex(result, prev.Str)
			if lastIdx >= 0 {
				result = result[:lastIdx] + prev.Str + op + next.Str
			}
			i++
			continue
		}

		// heredoc（<< 定界符）——定界符重建
		if op == "<" && next.IsOp("<") {
			if i+2 < len(kept) && kept[i+2].Kind == TokenKindStr {
				result = sqAddToken(result, kept[i+2].Str, false)
				i += 2
				continue
			}
		}

		// here-string 恒保留
		if op == "<<<" {
			result = sqAddToken(result, op, false)
			continue
		}

		// 括号（上游 1285-1311）
		if op == "(" {
			isCmdSub := sqDetectCommandSubstitution(prev, kept, i)
			if isCmdSub || cmdSubDepth > 0 {
				cmdSubDepth++
				// 命令替换无空格
				if strings.HasSuffix(result, " ") {
					result = result[:len(result)-1]
				}
				result += "("
			} else if strings.HasSuffix(result, "$") {
				// result 以 $ 结尾的命令替换形态
				if sqDetectCommandSubstitution(prev, kept, i) {
					cmdSubDepth++
					result += "("
				} else {
					result = sqAddToken(result, "(", false)
				}
			} else {
				noSpace := strings.HasSuffix(result, "<(") ||
					strings.HasSuffix(result, "(")
				result = sqAddToken(result, "(", noSpace)
			}
			continue
		}

		if op == ")" {
			if inProcessSub {
				inProcessSub = false
				result += ")"
				continue
			}
			if cmdSubDepth > 0 {
				cmdSubDepth--
			}
			result += ")"
			continue
		}

		// 进程替换
		if op == "<(" {
			inProcessSub = true
			result = sqAddToken(result, op, false)
			continue
		}

		// 其余操作符
		if op == "&&" || op == "||" || op == "|" || op == ";" ||
			op == ">" || op == ">>" || op == "<" {
			result = sqAddToken(result, op, false)
		}
	}

	out := strings.Trim(result, " \t\n")
	if out == "" {
		return originalCmd
	}
	return out
}
