// heredoc_extract.go——eva-cli utils/bash/heredoc.ts 的 Go 直译
// （文本级 heredoc 预提取/还原——shell-quote 配套设施，与 tree-sitter
// 语义的 heredoc.go 是两套东西）：
//   - ExtractHeredocs（113-687）：提取 heredoc 并替换为占位符
//   - RestoreHeredocs（711-720）：占位符还原
//   - ContainsHeredoc（731-733）：heredoc 语法存在性快检
//
// 安全语义（上游安全注释要点，直译保留）：
//   - $'...' / $"..."、首 << 前的反引号、未闭合 (( 算术上下文 → 整体
//     放弃提取（bail out）
//   - 引号内/注释内/奇数反斜杠前导的 << 不是 heredoc 操作符
//   - quotedOnly 模式跳过未引号 heredoc 但记录其体范围——嵌套引号
//     "heredoc" 被拒绝提取（防 $(evil) 被藏进占位符）
//   - 闭合定界符行前缀 PST_EOFTOKEN 形态（`)}|&;(<>）→ 放弃提取
//   - 同 contentStart 的多 heredoc / 体范围重叠 → 放弃提取
//
// HEREDOC_START_PATTERN（(?<!<)<<(?!<)(-)?[ \t]*(?:(['"])(\\?\w+)\2|
// \\?(\w+))）含 lookbehind/lookahead/反向引用——RE2 不支持，按语义
// 手工扫描实现（见 sqMatchHeredocStart）。

package parser

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
)

// HeredocInfo 单个 heredoc 的区间与定界符信息（对齐 HeredocInfo）。
type HeredocInfo struct {
	// FullText 完整 heredoc 文本（操作符 + 体，含闭合定界符行）
	FullText string
	// Delimiter 定界符词（不含引号）
	Delimiter string
	// OperatorStartIndex << 操作符在原命令中的起点
	OperatorStartIndex int
	// OperatorEndIndex << 操作符终点（排他）——同行后续内容保留
	OperatorEndIndex int
	// ContentStartIndex heredoc 体起点（体首行前的换行符处）
	ContentStartIndex int
	// ContentEndIndex heredoc 体终点（含闭合定界符行，排他）
	ContentEndIndex int
}

// HeredocExtractionResult 提取结果（对齐 HeredocExtractionResult）。
type HeredocExtractionResult struct {
	// ProcessedCommand heredoc 已替换为占位符的命令
	ProcessedCommand string
	// Heredocs 占位符 → heredoc 信息映射
	Heredocs map[string]HeredocInfo
}

const (
	sqHeredocPlaceholderPrefix = `__HEREDOC_`
	sqHeredocPlaceholderSuffix = `__`
)

// sqHeredocPlaceholderSalt 占位符随机盐（对齐 generatePlaceholderSalt
// ——8 字节 hex，防命令文本字面包含 __HEREDOC_N__ 的碰撞注入）
func sqHeredocPlaceholderSalt() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		// 随机源不可用——退化固定盐（占位符形态仍带序号，碰撞面可忽略）
		return "0000000000000000"
	}
	return hex.EncodeToString(buf)
}

// sqIsWordChar bash \w 等价（[A-Za-z0-9_]）
func sqIsWordChar(c byte) bool {
	return c == '_' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') ||
		(c >= 'A' && c <= 'Z')
}

// sqMatchHeredocStart 在 command 的 from 起点扫描下一个 heredoc 起始
// （对齐 HEREDOC_START_PATTERN 全局迭代一步）。返回 (matchStart,
// matchEnd, isDash, delimiter, quoteChar, found)。语义分解：
//   - (?<!<)：<< 左邻不是 <
//   - <<：字面双小于号
//   - (?!<)：<< 右邻不是 <
//   - (-)?：可选短横（<<- 剥前导 tab 形态）
//   - [ \t]*：仅水平空白（禁 \s——防跨行匹配藏命令）
//   - 引号形态：(['"]) (\\?\w+) \2——引号内定界符可含前导反斜杠，
//     且以同引号闭合
//   - 未引号形态：\\?(\w+)——可选转义反斜杠 + \w+
func sqMatchHeredocStart(command string, from int) (int, int, bool, string, byte, bool) {
	runes := command
	for i := from; i+1 < len(runes); {
		if runes[i] != '<' || runes[i+1] != '<' {
			// 快速推进到下一个 '<'
			next := strings.IndexByte(runes[i+1:], '<')
			if next < 0 {
				return 0, 0, false, "", 0, false
			}
			i = i + 1 + next
			continue
		}
		// (?<!<)：i-1 不是 <
		if i > 0 && runes[i-1] == '<' {
			i++
			continue
		}
		// (?!<)：i+2 不是 <
		if i+2 < len(runes) && runes[i+2] == '<' {
			i += 2
			continue
		}
		pos := i + 2
		isDash := false
		if pos < len(runes) && runes[pos] == '-' {
			isDash = true
			pos++
		}
		// [ \t]*
		for pos < len(runes) && (runes[pos] == ' ' || runes[pos] == '\t') {
			pos++
		}
		if pos >= len(runes) {
			i += 2
			continue
		}
		c := runes[pos]
		if c == '\'' || c == '"' {
			// 引号形态：(['"]) (\\?\w+) \2
			j := pos + 1
			delimStart := j
			if j < len(runes) && runes[j] == '\\' {
				j++
			}
			wordStart := j
			for j < len(runes) && sqIsWordChar(runes[j]) {
				j++
			}
			if j == wordStart {
				i++
				continue // \w+ 为空——非本形态
			}
			if j >= len(runes) || runes[j] != c {
				i++
				continue // 同引号未闭合
			}
			return i, j + 1, isDash, string(runes[delimStart:j]), c, true
		}
		// 未引号形态：\\?(\w+)
		j := pos
		if j < len(runes) && runes[j] == '\\' {
			j++
		}
		wordStart := j
		for j < len(runes) && sqIsWordChar(runes[j]) {
			j++
		}
		if j == wordStart {
			i++
			continue // \w+ 为空
		}
		return i, j, isDash, string(runes[wordStart:j]), 0, true
	}
	return 0, 0, false, "", 0, false
}

// sqHeredocScanner 增量引号/注释扫描器状态（对齐 extractHeredocs 的
// scanPos/scanInSingleQuote/scanInDoubleQuote/scanInComment/
// scanDqEscapeNext/scanPendingBackslashes——语义详见上游 186-229 安全
// 注释：引号态对注释盲、物理换行清注释态、奇数反斜杠串转义下字符）。
type sqHeredocScanner struct {
	command             string
	scanPos             int
	inSingle            bool
	inDouble            bool
	inComment           bool
	dqEscapeNext        bool
	pendingBackslashes  int
}

// advance 推进扫描器至 target（对齐 advanceScan 231-275）。
func (sc *sqHeredocScanner) advance(target int) {
	for i := sc.scanPos; i < target; i++ {
		ch := sc.command[i]

		// 物理换行清注释态（换行清位先于引号分支——对齐上游）
		if ch == '\n' {
			sc.inComment = false
		}

		if sc.inSingle {
			if ch == '\'' {
				sc.inSingle = false
			}
			continue
		}
		if sc.inDouble {
			if sc.dqEscapeNext {
				sc.dqEscapeNext = false
				continue
			}
			if ch == '\\' {
				sc.dqEscapeNext = true
				continue
			}
			if ch == '"' {
				sc.inDouble = false
			}
			continue
		}

		// 未引号上下文——引号态对注释盲（不因注释态跳过引号字符）
		if ch == '\\' {
			sc.pendingBackslashes++
			continue
		}
		escaped := sc.pendingBackslashes%2 == 1
		sc.pendingBackslashes = 0
		if escaped {
			continue
		}
		if ch == '\'' {
			sc.inSingle = true
		} else if ch == '"' {
			sc.inDouble = true
		} else if !sc.inComment && ch == '#' {
			sc.inComment = true
		}
	}
	sc.scanPos = target
}

// sqFindLogicalLineEnd 从 from 起找首个不在引号内的换行（对齐上游
// 399-435 的逻辑行扫描——多行引号串延伸逻辑行；引号内换行不算）。
// 返回相对偏移；-1 = 逻辑行未终结（无 heredoc 体）。
func sqFindLogicalLineEnd(command string, from int) int {
	inSingle, inDouble := false, false
	for k := from; k < len(command); k++ {
		ch := command[k]
		if inSingle {
			if ch == '\'' {
				inSingle = false
			}
			continue
		}
		if inDouble {
			if ch == '\\' {
				k++ // 跳过双引号内转义字符
				continue
			}
			if ch == '"' {
				inDouble = false
			}
			continue
		}
		if ch == '\n' {
			return k - from
		}
		// 未引号上下文：反斜杠转义判定（前导反斜杠计数）
		backslashCount := 0
		for j := k - 1; j >= from && command[j] == '\\'; j-- {
			backslashCount++
		}
		if backslashCount%2 == 1 {
			continue
		}
		if ch == '\'' {
			inSingle = true
		} else if ch == '"' {
			inDouble = true
		}
	}
	return -1
}

// ExtractHeredocs 提取 heredoc 并替换为占位符（对齐 extractHeredocs
// 113-687）。quotedOnly=true 时未引号 heredoc 不提取（其体范围进
// skipped 列表参与嵌套过滤——bashSecurity 同步版调用形态）。
func ExtractHeredocs(command string, quotedOnly bool) HeredocExtractionResult {
	result := HeredocExtractionResult{
		ProcessedCommand: command,
		Heredocs:         map[string]HeredocInfo{},
	}

	// 快检：无 << 直接返回
	if !strings.Contains(command, "<<") {
		return result
	}

	// 安全防线 1：$'...' / $"..."（ANSI-C/locale 引号）——扫描器不
	// 处理 $ 前缀引号，放弃提取
	if sqContainsDollarQuote(command) {
		return result
	}

	// 安全防线 2：首 << 前的反引号（反引号嵌套解析复杂且为
	// PST_EOFTOKEN 早闭形态，放弃提取；体内反引号无害）
	firstHeredocPos := strings.Index(command, "<<")
	if firstHeredocPos > 0 && strings.Contains(command[:firstHeredocPos], "`") {
		return result
	}

	// 安全防线 3：首 << 前未闭合的 (( 算术上下文（<< 可能是位移运算
	// 符而非 heredoc——误提取会把后续行藏进"体"，放弃提取）
	if firstHeredocPos > 0 {
		before := command[:firstHeredocPos]
		openArith := strings.Count(before, "((")
		closeArith := strings.Count(before, "))")
		if openArith > closeArith {
			return result
		}
	}

	var matches []HeredocInfo
	// quotedOnly 模式跳过的未引号 heredoc 体范围（嵌套过滤用）
	type skippedRange struct{ start, end int }
	var skipped []skippedRange

	scanner := &sqHeredocScanner{command: command}
	searchFrom := 0
	for {
		start, end, isDash, delimiter, quoteChar, found :=
			sqMatchHeredocStart(command, searchFrom)
		if !found {
			break
		}
		searchFrom = start + 2 // 从 << 之后继续（防零推进死循环）

		// 推进扫描器至本匹配点——此后引号/注释/反斜杠态即匹配点前
		// 的解析状态
		scanner.advance(start)

		// 引号内 << 非 heredoc 操作符
		if scanner.inSingle || scanner.inDouble {
			continue
		}
		// 注释内 <<（bash 中 # <<EOF 是注释——提取会把后续行藏进"体"）
		if scanner.inComment {
			continue
		}
		// 奇数反斜杠前导（\< 字面量——\<EOF 是输入重定向非 heredoc）
		if scanner.pendingBackslashes%2 == 1 {
			continue
		}
		// 前置跳过 heredoc（quotedOnly 未引号形态）体内的 << 是文本
		// 不是操作符
		insideSkipped := false
		for _, sk := range skipped {
			if start > sk.start && start < sk.end {
				insideSkipped = true
				break
			}
		}
		if insideSkipped {
			continue
		}

		// 校验 1：引号形态必须真闭合（\w+ 提前停止会留下未消费的
		// 闭合引号——如 <<"EO F"，定界符差异即走私面）
		if quoteChar != 0 && command[end-1] != quoteChar {
			continue
		}

		// 定界符是否引号/转义形态（体内容全字面——quotedOnly 的跳过
		// 判定基准）
		isEscapedDelimiter := strings.Contains(command[start:end], `\`)
		isQuotedOrEscaped := quoteChar != 0 || isEscapedDelimiter

		// 校验 2：匹配终点必须是 bash 词终结符（[ \t\n|&;()<>] 或串
		// 尾）——<<'EOF'a 的 bash 定界符是 EOFa，捕获 EOF 即差异
		if end < len(command) {
			nextChar := command[end]
			if !strings.ContainsRune(" \t\n|&;()<>", rune(nextChar)) {
				continue
			}
		}

		// heredoc 体起于下一逻辑行——同行后续（&& echo done 等）属于
		// 命令非体。逻辑行终点须引号感知（上游 386-398 走私样例）
		firstNewlineOffset := sqFindLogicalLineEnd(command, end)
		if firstNewlineOffset == -1 {
			continue
		}

		// 同行内容以奇数反斜杠收尾 = 行连续（heredoc 先于连续合并的
		// 处理序会误解析，放弃提取；上游 442-471）
		sameLineContent := command[end : end+firstNewlineOffset]
		trailingBackslashes := 0
		for j := len(sameLineContent) - 1; j >= 0; j-- {
			if sameLineContent[j] == '\\' {
				trailingBackslashes++
			} else {
				break
			}
		}
		if trailingBackslashes%2 == 1 {
			continue
		}

		contentStart := end + firstNewlineOffset
		afterNewline := command[contentStart+1:]
		contentLines := strings.Split(afterNewline, "\n")

		// 找闭合定界符行：<< 要求定界符独占整行；<<- 剥前导 tab。
		// PST_EOFTOKEN 早闭形态（定界符开头 + `)}|&;(<> 字符）→ 放弃
		closingLineIndex := -1
		for i, line := range contentLines {
			var checkLine string
			if isDash {
				checkLine = strings.TrimLeft(line, "\t")
				if checkLine == delimiter {
					closingLineIndex = i
					break
				}
			} else {
				if line == delimiter {
					closingLineIndex = i
					break
				}
				checkLine = line
			}
			// 早闭形态检查（<<- 同样剥 tab 后检查）
			if isDash {
				checkLine = strings.TrimLeft(line, "\t")
			}
			if len(checkLine) > len(delimiter) &&
				strings.HasPrefix(checkLine, delimiter) {
				charAfter := checkLine[len(delimiter)]
				if strings.ContainsRune(")}`|&;(<>", rune(charAfter)) {
					closingLineIndex = -1
					break
				}
			}
		}

		// quotedOnly：未引号 heredoc 记录体范围后跳过（先于闭合缺失
		// 检查——无闭合时体延伸至串尾，仍需阻断其内嵌套提取）
		if quotedOnly && !isQuotedOrEscaped {
			var skipEnd int
			if closingLineIndex == -1 {
				skipEnd = len(command)
			} else {
				skipLen := len(strings.Join(contentLines[:closingLineIndex+1], "\n"))
				skipEnd = contentStart + 1 + skipLen
			}
			skipped = append(skipped, skippedRange{contentStart, skipEnd})
			continue
		}

		// 无闭合定界符 = 畸形，跳过
		if closingLineIndex == -1 {
			continue
		}

		linesUpToClosing := strings.Join(contentLines[:closingLineIndex+1], "\n")
		contentEnd := contentStart + 1 + len(linesUpToClosing)

		// 体范围与已跳过范围重叠（同行多 heredoc + 首个未引号形态的
		// 顺序体错位——上游 565-598 样例）→ 放弃提取
		overlapsSkipped := false
		for _, sk := range skipped {
			if contentStart < sk.end && sk.start < contentEnd {
				overlapsSkipped = true
				break
			}
		}
		if overlapsSkipped {
			continue
		}

		fullText := command[start:end] + command[contentStart:contentEnd]
		matches = append(matches, HeredocInfo{
			FullText:           fullText,
			Delimiter:          delimiter,
			OperatorStartIndex: start,
			OperatorEndIndex:   end,
			ContentStartIndex:  contentStart,
			ContentEndIndex:    contentEnd,
		})
	}

	if len(matches) == 0 {
		return result
	}

	// 嵌套过滤：操作符起点落在其他 heredoc 体范围内的候选剔除
	var topLevel []HeredocInfo
	for _, candidate := range matches {
		nested := false
		for _, other := range matches {
			if candidate == other {
				continue
			}
			if candidate.OperatorStartIndex > other.ContentStartIndex &&
				candidate.OperatorStartIndex < other.ContentEndIndex {
				nested = true
				break
			}
		}
		if !nested {
			topLevel = append(topLevel, candidate)
		}
	}
	if len(topLevel) == 0 {
		return result
	}

	// 同 contentStart 的多 heredoc（同线多 heredoc 索引腐坏）→ 放弃
	startsSeen := map[int]bool{}
	for _, h := range topLevel {
		startsSeen[h.ContentStartIndex] = true
	}
	if len(startsSeen) < len(topLevel) {
		return result
	}

	// 按体终点降序替换（保序前面的替换索引）
	// 简单插入排序降序（规模小）
	for i := 1; i < len(topLevel); i++ {
		for j := i; j > 0 && topLevel[j].ContentEndIndex > topLevel[j-1].ContentEndIndex; j-- {
			topLevel[j], topLevel[j-1] = topLevel[j-1], topLevel[j]
		}
	}

	salt := sqHeredocPlaceholderSalt()
	processed := command
	for i, info := range topLevel {
		placeholderIndex := len(topLevel) - 1 - i
		placeholder := sqHeredocPlaceholderPrefix + sqItoa(placeholderIndex) +
			"_" + salt + sqHeredocPlaceholderSuffix
		result.Heredocs[placeholder] = info
		// 替换保同行内容：操作符前原样 + 占位符 + 操作符到体起点原样
		// + 体终点后原样
		processed = processed[:info.OperatorStartIndex] + placeholder +
			processed[info.OperatorEndIndex:info.ContentStartIndex] +
			processed[info.ContentEndIndex:]
	}

	result.ProcessedCommand = processed
	return result
}

// sqContainsDollarQuote $'...' / $"..." 形态检测（对齐 /\$['"]/.test）
func sqContainsDollarQuote(command string) bool {
	for i := 0; i < len(command); i++ {
		if command[i] == '$' && i+1 < len(command) &&
			(command[i+1] == '\'' || command[i+1] == '"') {
			return true
		}
	}
	return false
}

// RestoreHeredocsInString 单串占位符还原（对齐 restoreHeredocsInString
// 693-702——遍历替换）
func RestoreHeredocsInString(text string, heredocs map[string]HeredocInfo) string {
	result := text
	for placeholder, info := range heredocs {
		result = strings.ReplaceAll(result, placeholder, info.FullText)
	}
	return result
}

// RestoreHeredocs 串数组占位符还原（对齐 restoreHeredocs 711-720）
func RestoreHeredocs(parts []string, heredocs map[string]HeredocInfo) []string {
	if len(heredocs) == 0 {
		return parts
	}
	out := make([]string, len(parts))
	for i, p := range parts {
		out[i] = RestoreHeredocsInString(p, heredocs)
	}
	return out
}

// ContainsHeredoc heredoc 语法存在性快检（对齐 containsHeredoc 731-733
// ——只验模式存在，不验 well-formed）
func ContainsHeredoc(command string) bool {
	_, _, _, _, _, found := sqMatchHeredocStart(command, 0)
	return found
}

// ── 守卫语义版起始匹配与闭合定位（v0.5.1）──
//
// 上游 programWriteGuard.ts 自带独立于 heredoc.ts 的 heredoc 检测
// （HEREDOC_START_RE `<<-?\s*(['"]?)(\w+)\1[^\n]*\n` + stripHeredoc
// Bodies 闭合行语义）——与 heredoc.ts 精确安全语义为上游固有双版
//（执行面写意图检测 vs 安全面提取）。本组导出为守卫语义的直译，
// 与私有 sqMatchHerdodocStart 并存属上游固有分层（非重复）。

// MatchHeredocStart heredoc 起始匹配（守卫语义版，对齐
// programWriteGuard.ts HEREDOC_START_RE——无 lookaround，反向引用
// 手工化；\s* 为 JS 全集含换行按上游原样）。返回 (matchStart,
// bodyStart, delimiter, ok)——bodyStart = 起始行换行符之后
// （匹配消费 `[^\n]*\n`）。
func MatchHeredocStart(s string, from int) (int, int, string, bool) {
	for i := from; i+1 < len(s); {
		if s[i] != '<' || s[i+1] != '<' {
			next := strings.IndexByte(s[i+1:], '<')
			if next < 0 {
				return 0, 0, "", false
			}
			i = i + 1 + next
			continue
		}
		pos := i + 2
		if pos < len(s) && s[pos] == '-' {
			pos++
		}
		// \s*（JS 全集，含换行——上游原样）
		for pos < len(s) && IsJSSpace(rune(s[pos])) {
			pos++
		}
		if pos >= len(s) {
			return 0, 0, "", false
		}
		// (['"]?)(\w+)\1：引号可选且须配对（反向引用手工化）
		quote := byte(0)
		if s[pos] == '\'' || s[pos] == '"' {
			quote = s[pos]
			pos++
		}
		wordStart := pos
		for pos < len(s) && sqIsWordChar(s[pos]) {
			pos++
		}
		if pos == wordStart {
			i++
			continue // \w+ 为空
		}
		delim := s[wordStart:pos]
		if quote != 0 {
			if pos < len(s) && s[pos] == quote {
				pos++
			} else {
				i++
				continue // 配对引号未闭合
			}
		}
		// [^\n]*\n：起始行余部 + 换行（无换行 = 无体，非本形态）
		nl := strings.IndexByte(s[pos:], '\n')
		if nl < 0 {
			i++
			continue
		}
		bodyStart := pos + nl + 1
		return i, bodyStart, delim, true
	}
	return 0, 0, "", false
}

// FindHeredocCloseLine 在 bodyStart 起逐行找闭合标记行，返回体终点
// （闭合行尾，排他）；-1 = 未找到。闭合语义对齐 programWriteGuard.ts
// 的 closeRe `^[ \t]*DELIM[\s&|;)]*$`（m 模式；\s 为 JS 全集）。
func FindHeredocCloseLine(s string, bodyStart int, delim string) int {
	lines := strings.SplitAfter(s[bodyStart:], "\n")
	offset := 0
	for _, line := range lines {
		lineEnd := len(line) // SplitAfter 保留 \n——行内容含尾换行
		content := strings.TrimRight(line, "\n")
		trimmed := strings.TrimLeft(content, " \t")
		if strings.HasPrefix(trimmed, delim) {
			tail := trimmed[len(delim):]
			if sqGuardCloseTailOK(tail) {
				return bodyStart + offset + lineEnd
			}
		}
		offset += lineEnd
	}
	return -1
}

// sqGuardCloseTailOK 闭合行尾判定（[JS\s&|;)]* 到行尾——\s 走
// jsspace 权威全集）
func sqGuardCloseTailOK(tail string) bool {
	for i := 0; i < len(tail); i++ {
		c := rune(tail[i])
		if !(c == '&' || c == '|' || c == ';' || c == ')' || c == '`' ||
			IsJSSpace(c)) {
			return false
		}
	}
	return true
}
