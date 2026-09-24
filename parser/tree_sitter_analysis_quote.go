// tree_sitter_analysis_quote.go——上游 eva-cli utils/bash/treeSitterAnalysis.ts
// 21-290 行（QuoteContext 类型 + collectQuoteSpans + span 代数 +
// extractQuoteContext）的 Go 直译。
//
// 索引空间差异登记：上游 JS 侧 tree-sitter 产 UTF-8 字节偏移，但
// extractQuoteContext 逐字符遍历用 UTF-16 码元索引比较 span 位置——
// ASCII 下两者一致，命令含多字节字符时上游 span 与字符索引错位。
// Go 侧 string 索引即 UTF-8 字节偏移，多字节场景天然正确
//（方向为 Go 修正，登记于已知差异）。

package parser

// span 字节偏移区间 [start, end)
type span struct {
	start, end int
}

// QuoteContext 引号上下文（对齐 treeSitterAnalysis.ts QuoteContext）。
type QuoteContext struct {
	// WithDoubleQuotes 移除单引号内容（双引号内容保留）
	WithDoubleQuotes string `json:"withDoubleQuotes"`
	// FullyUnquoted 移除全部引号内容
	FullyUnquoted string `json:"fullyUnquoted"`
	// UnquotedKeepQuoteChars 同上但保留引号字符（'、"）
	UnquotedKeepQuoteChars string `json:"unquotedKeepQuoteChars"`
}

// quoteSpans 单趟收集的各类引号区间（对齐 QuoteSpans）。
type quoteSpans struct {
	raw     []span // raw_string（单引号）
	ansiC   []span // ansi_c_string（$'...'）
	double  []span // string（双引号）
	heredoc []span // 引号型 heredoc_redirect
}

// collectQuoteSpans 单趟收集全部引号相关区间（对齐
// collectQuoteSpans——融合遍历替代逐类型 5 次走树）。
//
// 复刻逐类型走树语义：每种走树只在自己类型处停。raw_string 走树会
// 穿过 string 节点抵达 $(...) 内嵌 raw_string，而 string 走树在外层
// string 停。inDouble 标记用于按路径收集最外层 string 区间，同时仍
// 下钻 $()/${} 体收集内层 raw_string/ansi_c_string。
//
// raw_string / ansi_c_string / 引号 heredoc 体在 bash 中为字面文本
// （无展开），不存在嵌套引号节点——提前返回。
func collectQuoteSpans(node *TsNode, out *quoteSpans, inDouble bool) {
	switch node.Type {
	case "raw_string":
		out.raw = append(out.raw, span{node.StartIndex, node.EndIndex})
		return // 字面体，不可能有嵌套引号
	case "ansi_c_string":
		out.ansiC = append(out.ansiC, span{node.StartIndex, node.EndIndex})
		return // 字面体
	case "string":
		// 只收集最外层 string（对齐旧逐类型走树在首个匹配处停）。
		// 无条件下钻——"..." 内嵌的 $(cmd 'x') 有真实内层 raw_string。
		if !inDouble {
			out.double = append(out.double, span{node.StartIndex, node.EndIndex})
		}
		for _, child := range node.Children {
			collectQuoteSpans(child, out, true)
		}
		return
	case "heredoc_redirect":
		// 引号 heredoc（<<'EOF'、<<"EOF"、<<\EOF）：字面体。
		// 非引号（<<EOF）会展开 $()/${}——体可含 $(cmd 'x')，其内层
		// '...' 是真实 raw_string 节点。判定：heredoc_start 文本以 '/"/\ 开头。
		// 对齐同步路径 extractHeredocs({ quotedOnly: true })。
		isQuoted := false
		for _, child := range node.Children {
			if child.Type == "heredoc_start" {
				first := child.Text[0]
				isQuoted = first == '\'' || first == '"' || first == '\\'
				break
			}
		}
		if isQuoted {
			out.heredoc = append(out.heredoc, span{node.StartIndex, node.EndIndex})
			return // 字面体，无嵌套引号节点
		}
		// 非引号：下钻 heredoc_body → command_substitution → 内层引号节点。
		// 原逐类型走树不在 heredoc_redirect 停（不在类型集内），此处同样穿透。
	}
	for _, child := range node.Children {
		collectQuoteSpans(child, out, inDouble)
	}
}

// buildPositionSet 构建区间覆盖的全部字节位置集合（对齐 buildPositionSet）。
func buildPositionSet(spans []span) map[int]bool {
	set := make(map[int]bool)
	for _, s := range spans {
		for i := s.start; i < s.end; i++ {
			set[i] = true
		}
	}
	return set
}

// dropContainedSpans 去除被其他区间完全包含的内层区间，只留最外层
// （对齐 dropContainedSpans）。嵌套引号（如 "$(echo 'hi')"）产生重叠
// 区间——内层 raw_string 由外层 string 节点下钻找到。处理重叠区间会
// 因外层删除导致的位移使内层索引失效。
func dropContainedSpans(spans []span) []span {
	kept := make([]span, 0, len(spans))
	for i, s := range spans {
		contained := false
		for j, other := range spans {
			if j != i &&
				other.start <= s.start && other.end >= s.end &&
				(other.start < s.start || other.end > s.end) {
				contained = true
				break
			}
		}
		if !contained {
			kept = append(kept, s)
		}
	}
	return kept
}

// removeSpans 从字符串移除区间（对齐 removeSpans）。
func removeSpans(command string, spans []span) string {
	if len(spans) == 0 {
		return command
	}
	// 去内层包含区间后按 start 降序，切片拼接无位移问题。
	sorted := dropContainedSpans(spans)
	sortSpansDesc(sorted)
	result := command
	for _, s := range sorted {
		result = result[:s.start] + result[s.end:]
	}
	return result
}

// quotedSpan 带定界符替换信息的区间（对齐 [start, end, open, close] 四元组）。
type quotedSpan struct {
	span
	open, close string
}

// replaceSpansKeepQuotes 将区间替换为仅引号定界符（对齐
// replaceSpansKeepQuotes）。
func replaceSpansKeepQuotes(command string, spans []quotedSpan) string {
	if len(spans) == 0 {
		return command
	}
	// 先去内层包含区间再降序
	base := make([]span, len(spans))
	for i, s := range spans {
		base[i] = s.span
	}
	keptIdx := dropContainedIdx(base)
	sorted := make([]quotedSpan, 0, len(keptIdx))
	for _, idx := range keptIdx {
		sorted = append(sorted, spans[idx])
	}
	sortQuotedSpansDesc(sorted)
	result := command
	for _, s := range sorted {
		// 替换内容但保留定界符
		result = result[:s.start] + s.open + s.close + result[s.end:]
	}
	return result
}

// sortSpansDesc 按 start 降序（对齐 sort((a,b) => b[0]-a[0])）。
func sortSpansDesc(spans []span) {
	for i := 1; i < len(spans); i++ {
		for j := i; j > 0 && spans[j].start > spans[j-1].start; j-- {
			spans[j], spans[j-1] = spans[j-1], spans[j]
		}
	}
}

// sortQuotedSpansDesc 同上，quotedSpan 版。
func sortQuotedSpansDesc(spans []quotedSpan) {
	for i := 1; i < len(spans); i++ {
		for j := i; j > 0 && spans[j].start > spans[j-1].start; j-- {
			spans[j], spans[j-1] = spans[j-1], spans[j]
		}
	}
}

// dropContainedIdx 返回未被包含的区间下标（保持与 spans 的对应关系）。
func dropContainedIdx(spans []span) []int {
	idx := make([]int, 0, len(spans))
	for i, s := range spans {
		contained := false
		for j, other := range spans {
			if j != i &&
				other.start <= s.start && other.end >= s.end &&
				(other.start < s.start || other.end > s.end) {
				contained = true
				break
			}
		}
		if !contained {
			idx = append(idx, i)
		}
	}
	return idx
}

// ExtractQuoteContext 从 AST 提取引号上下文（对齐 extractQuoteContext）。
//
// tree-sitter 节点类型：
//   - raw_string: 单引号 ('...')
//   - string: 双引号 ("...")
//   - ansi_c_string: ANSI-C 引用（$'...'）——区间含前导 $
//   - heredoc_redirect: 仅引号 heredoc（<<'EOF' 等）——整个重定向区间
//     （<<、定界符、体、换行）全部剥离，因为体是字面文本。非引号
//     heredoc（<<EOF）保留原位，bash 会展开其中的 $(...)/${...}，
//     校验器需要看到这些形态。
func ExtractQuoteContext(rootNode *TsNode, command string) QuoteContext {
	// 单趟收集全部引号区间类型
	sp := &quoteSpans{}
	collectQuoteSpans(rootNode, sp, false)
	singleQuoteSpans := sp.raw
	ansiCSpans := sp.ansiC
	doubleQuoteSpans := sp.double
	quotedHeredocSpans := sp.heredoc

	// 为每个输出变体构建排除位置集合。
	// WithDoubleQuotes：完全移除单引号区间，外加双引号区间的首尾 "
	// 定界符（保留其间内容）。对齐 regex extractQuotedContent() 语义：
	// " 切换引号状态但内容仍输出。
	singleQuoteSet := buildPositionSet(append(
		append(append([]span{}, singleQuoteSpans...), ansiCSpans...),
		quotedHeredocSpans...))
	doubleQuoteDelimSet := make(map[int]bool)
	for _, s := range doubleQuoteSpans {
		doubleQuoteDelimSet[s.start] = true // 开 "
		doubleQuoteDelimSet[s.end-1] = true // 闭 "
	}
	var withDouble []byte
	for i := 0; i < len(command); i++ {
		if singleQuoteSet[i] || doubleQuoteDelimSet[i] {
			continue
		}
		withDouble = append(withDouble, command[i])
	}

	// FullyUnquoted：移除全部引号内容
	allQuoteSpans := make([]span, 0,
		len(singleQuoteSpans)+len(ansiCSpans)+len(doubleQuoteSpans)+len(quotedHeredocSpans))
	allQuoteSpans = append(allQuoteSpans, singleQuoteSpans...)
	allQuoteSpans = append(allQuoteSpans, ansiCSpans...)
	allQuoteSpans = append(allQuoteSpans, doubleQuoteSpans...)
	allQuoteSpans = append(allQuoteSpans, quotedHeredocSpans...)
	fullyUnquoted := removeSpans(command, allQuoteSpans)

	// UnquotedKeepQuoteChars：移除内容但保留定界符字符
	spansWithQuoteChars := make([]quotedSpan, 0,
		len(allQuoteSpans))
	for _, s := range singleQuoteSpans {
		spansWithQuoteChars = append(spansWithQuoteChars,
			quotedSpan{s, "'", "'"})
	}
	for _, s := range ansiCSpans {
		// ansi_c_string 区间含前导 $；保留它以对齐 regex 路径——
		// regex 路径把 $ 视为 ' 前的非引号字符。
		spansWithQuoteChars = append(spansWithQuoteChars,
			quotedSpan{s, "$'", "'"})
	}
	for _, s := range doubleQuoteSpans {
		spansWithQuoteChars = append(spansWithQuoteChars,
			quotedSpan{s, "\"", "\""})
	}
	for _, s := range quotedHeredocSpans {
		// heredoc 重定向区间没有行内引号定界符——整体剥离。
		spansWithQuoteChars = append(spansWithQuoteChars,
			quotedSpan{s, "", ""})
	}
	unquotedKeepQuoteChars := replaceSpansKeepQuotes(command, spansWithQuoteChars)

	return QuoteContext{
		WithDoubleQuotes:       string(withDouble),
		FullyUnquoted:          fullyUnquoted,
		UnquotedKeepQuoteChars: unquotedKeepQuoteChars,
	}
}
