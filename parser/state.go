package parser

import "time"

// state.go：解析状态与节点构造（对齐 bashParser.ts 593-704 行）。

// TsNode AST 节点（对齐上游 TsNode；JSON 字段名与 TS dump 一致）
type TsNode struct {
	Type       string    `json:"type"`
	Text       string    `json:"text"`
	StartIndex int       `json:"startIndex"`
	EndIndex   int       `json:"endIndex"`
	Children   []*TsNode `json:"children"`
}

const (
	parseTimeoutMs = 50    // 对齐 PARSE_TIMEOUT_MS
	maxNodes       = 50000 // 对齐 MAX_NODES
)

// budgetError 预算超限 panic 信号（parseSource recover 转 nil）
type budgetError struct{ kind string }

func (e *budgetError) Error() string { return e.kind }

// ParseState 解析状态（对齐上游 ParseState）
type ParseState struct {
	L          *Lexer
	src        string
	srcRunes   []rune
	srcBytes   int
	isAscii    bool   // 字节偏移 == rune 索引（无多字节 UTF-8）
	nodeCount  int
	deadline   time.Time
	aborted    bool
	inBacktick int    // 反引号嵌套深度——`...` 内 ` 终止词
	stopToken  string // 非空时 parseSimpleCommand 在此 token 停（`[` 回溯）
}

// ParseSource 解析入口（对齐 parseSource；timeoutMs 0 取默认 50ms；
// 中止/异常返回 nil）
func ParseSource(source string, timeoutMs int) (root *TsNode) {
	L := MakeLexer(source)
	p := &ParseState{
		L:        L,
		src:      source,
		srcRunes: []rune(source),
		srcBytes: len(source), // Go string len 即 UTF-8 字节数
		isAscii:  len(source) == len([]rune(source)),
	}
	d := timeoutMs
	if d <= 0 {
		d = parseTimeoutMs
	}
	p.deadline = time.Now().Add(time.Duration(d) * time.Millisecond)
	defer func() {
		if r := recover(); r != nil {
			root = nil
		}
	}()
	program := parseProgram(p)
	if p.aborted {
		return nil
	}
	return program
}

// checkBudget 节点预算 + 每 128 节点的时钟检查（对齐 checkBudget）
func checkBudget(p *ParseState) {
	p.nodeCount++
	if p.nodeCount > maxNodes {
		p.aborted = true
		panic(&budgetError{kind: "budget"})
	}
	if p.nodeCount&0x7f == 0 && time.Now().After(p.deadline) {
		p.aborted = true
		panic(&budgetError{kind: "timeout"})
	}
}

// mk 构造节点（text 按字节区间切片；children nil 规范化为空切片——
// JSON 序列化对齐 TS 的 []）
func mk(p *ParseState, typ string, start, end int, children []*TsNode) *TsNode {
	checkBudget(p)
	if children == nil {
		children = []*TsNode{}
	}
	return &TsNode{
		Type:       typ,
		Text:       sliceBytes(p, start, end),
		StartIndex: start,
		EndIndex:   end,
		Children:   children,
	}
}

// sliceBytes 字节区间 → 源文本（对齐 sliceBytes：rune 偏移二分查找）
func sliceBytes(p *ParseState, startByte, endByte int) string {
	if p.isAscii {
		return p.src[startByte:endByte]
	}
	L := p.L
	if L.byteTable == nil {
		byteAtChar(L, 0)
	}
	t := L.byteTable
	idx := func(targetByte int) int {
		lo, hi := 0, len(p.srcRunes)
		for lo < hi {
			m := (lo + hi) >> 1
			if t[m] < targetByte {
				lo = m + 1
			} else {
				hi = m
			}
		}
		return lo
	}
	return string(p.srcRunes[idx(startByte):idx(endByte)])
}

// leaf 从 token 构造叶子节点
func leaf(p *ParseState, typ string, tok Token) *TsNode {
	return mk(p, typ, tok.Start, tok.End, nil)
}

// lexSave 词法位置快照（上游打包为 (b<<16)|i——i 超 65535 溢出；
// Go 用结构体，语义等价且无溢出限制）
type lexSave struct {
	i int
	b int
}

func saveLex(L *Lexer) lexSave { return lexSave{i: L.i, b: L.b} }

func restoreLex(L *Lexer, s lexSave) {
	L.i = s.i
	L.b = s.b
}

// restoreLexToByte 按字节偏移恢复词法位置（对齐 restoreLexToByte）
func restoreLexToByte(p *ParseState, targetByte int) {
	if p.L.byteTable == nil {
		byteAtChar(p.L, 0)
	}
	t := p.L.byteTable
	lo, hi := 0, len(p.srcRunes)
	for lo < hi {
		m := (lo + hi) >> 1
		if t[m] < targetByte {
			lo = m + 1
		} else {
			hi = m
		}
	}
	p.L.i = lo
	p.L.b = targetByte
}
