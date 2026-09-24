// parsed_command.go——上游 eva-cli utils/bash/ParsedCommand.ts（319 行）的
// Go 直译：命令级视图（管道分段/输出重定向剥离/复合结构分析）。
//
// 范围登记：上游双实现中的 RegexParsedCommand_DEPRECATED（regex/shell-quote
// 回退版）服务主门不可用场景；Go 侧库静态链接恒可用，主门失败仅剩
// PARSE_ABORTED（fail-closed，不得路由回退——上游 parser.ts 注释明示），
// 故 Parse 入口在主门失败时返回 nil。
package parser

import (
	"regexp"
	"strings"
	"sync"
)

// OutputRedirection 输出重定向（对齐 ParsedCommand.ts OutputRedirection）。
type OutputRedirection struct {
	Target   string
	Operator string // ">" 或 ">>"
}

// ParsedCommand 命令级视图接口（对齐 IParsedCommand）。
type ParsedCommand interface {
	// OriginalCommand 原始命令文本
	OriginalCommand() string
	// PipeSegments 管道分段
	PipeSegments() []string
	// WithoutOutputRedirections 剥离输出重定向后的命令
	WithoutOutputRedirections() string
	// OutputRedirections 输出重定向列表
	OutputRedirections() []OutputRedirection
	// TreeSitterAnalysis 主门分析数据
	TreeSitterAnalysis() *Analysis
}

// redirectionNode 含位置的重定向（对齐 RedirectionNode）。
type redirectionNode struct {
	OutputRedirection
	startIndex, endIndex int
}

// treeSitterParsedCommand 主门实现（对齐 TreeSitterParsedCommand）。
type treeSitterParsedCommand struct {
	original string
	pipePos  []int
	redirs   []redirectionNode
	analysis *Analysis
}

func (t *treeSitterParsedCommand) OriginalCommand() string { return t.original }

// PipeSegments 按管道位置切片（对齐 getPipeSegments：字节偏移切片，
// 空段跳过）。Go string 切片即 UTF-8 字节偏移，与节点偏移一致
// （上游 JS 需 Buffer 桥接 UTF-16/UTF-8 差异，Go 天然无此问题）。
func (t *treeSitterParsedCommand) PipeSegments() []string {
	if len(t.pipePos) == 0 {
		return []string{t.original}
	}

	var segments []string
	currentStart := 0

	for _, pipePos := range t.pipePos {
		segment := strings.TrimSpace(t.original[currentStart:pipePos])
		if segment != "" {
			segments = append(segments, segment)
		}
		currentStart = pipePos + 1
	}

	lastSegment := strings.TrimSpace(t.original[currentStart:])
	if lastSegment != "" {
		segments = append(segments, lastSegment)
	}

	return segments
}

// whitespaceRe 多空白折叠（对齐 replace(/\s+/g, ' ')）
var whitespaceRe = regexp.MustCompile(`\s+`)

// WithoutOutputRedirections 剥离输出重定向（对齐
// withoutOutputRedirections：按位置倒序切除，trim，多空白折叠为单空格）。
func (t *treeSitterParsedCommand) WithoutOutputRedirections() string {
	if len(t.redirs) == 0 {
		return t.original
	}

	// 按 startIndex 降序切除（对齐 sort((a,b) => b.startIndex-a.startIndex)）
	sorted := make([]redirectionNode, len(t.redirs))
	copy(sorted, t.redirs)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j].startIndex > sorted[j-1].startIndex; j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}

	result := t.original
	for _, r := range sorted {
		result = result[:r.startIndex] + result[r.endIndex:]
	}
	return whitespaceRe.ReplaceAllString(strings.TrimSpace(result), " ")
}

func (t *treeSitterParsedCommand) OutputRedirections() []OutputRedirection {
	out := make([]OutputRedirection, len(t.redirs))
	for i, r := range t.redirs {
		out[i] = r.OutputRedirection
	}
	return out
}

func (t *treeSitterParsedCommand) TreeSitterAnalysis() *Analysis { return t.analysis }

// visitNodes 深度优先遍历（对齐 visitNodes）。
func visitNodes(node *TsNode, visitor func(*TsNode)) {
	visitor(node)
	for _, child := range node.Children {
		visitNodes(child, visitor)
	}
}

// extractPipePositions 收集管道位置（对齐 extractPipePositions）。
//
// visitNodes 为深度优先。`a | b && c | d` 中外层 list 把第二个 pipeline
// 作为第一个的兄弟嵌套，外层 | 先于内层 | 被访问——位置乱序。
// getPipeSegments 需从左到右切片，故此处排序。
func extractPipePositions(rootNode *TsNode) []int {
	var pipePositions []int
	visitNodes(rootNode, func(node *TsNode) {
		if node.Type == "pipeline" {
			for _, child := range node.Children {
				if child.Type == "|" {
					pipePositions = append(pipePositions, child.StartIndex)
				}
			}
		}
	})
	sortInts(pipePositions)
	return pipePositions
}

// extractRedirectionNodes 收集输出重定向节点（对齐 extractRedirectionNodes：
// file_redirect 下 op（'>'/'>>'）与 target（word））。
func extractRedirectionNodes(rootNode *TsNode) []redirectionNode {
	var redirections []redirectionNode
	visitNodes(rootNode, func(node *TsNode) {
		if node.Type == "file_redirect" {
			var op, target *TsNode
			for _, c := range node.Children {
				if c.Type == ">" || c.Type == ">>" {
					op = c
				} else if c.Type == "word" && target == nil {
					target = c
				}
			}
			if op != nil && target != nil {
				redirections = append(redirections, redirectionNode{
					OutputRedirection: OutputRedirection{
						Target:   target.Text,
						Operator: op.Type,
					},
					startIndex: node.StartIndex,
					endIndex:   node.EndIndex,
				})
			}
		}
	})
	return redirections
}

// sortInts 升序（对齐 sort((a,b) => a-b)）。
func sortInts(xs []int) {
	for i := 1; i < len(xs); i++ {
		for j := i; j > 0 && xs[j] < xs[j-1]; j-- {
			xs[j], xs[j-1] = xs[j-1], xs[j]
		}
	}
}

// BuildParsedCommandFromRoot 从已解析 AST 根构建（对齐
// buildParsedCommandFromRoot——调用方已有树时跳过冗余 parse）。
func BuildParsedCommandFromRoot(command string, root *TsNode) ParsedCommand {
	pipePositions := extractPipePositions(root)
	redirectionNodes := extractRedirectionNodes(root)
	analysis := AnalyzeCommand(root, command)
	return &treeSitterParsedCommand{
		original: command,
		pipePos:  pipePositions,
		redirs:   redirectionNodes,
		analysis: &analysis,
	}
}

// 单项缓存（对齐 ParsedCommand.ts 292-298 行）：旧调用方可能用同一
// 命令串反复调 Parse——每次 parse 约 1 次 parse + 6 次树遍历，缓存
// 最近一条命令跳过冗余工作。容量 1 防止实例泄漏。
var (
	cacheMu   sync.Mutex
	lastCmd   string
	cacheSeen bool
	lastRes   ParsedCommand
)

// Parse 解析命令字符串（对齐 ParsedCommand.parse）。主门失败（空/超长/
// 中止）返回 nil——无回退路径（见文件头范围登记）。
func Parse(command string) ParsedCommand {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	if cacheSeen && command == lastCmd {
		return lastRes
	}
	res := doParse(command)
	lastCmd = command
	cacheSeen = true
	lastRes = res
	return res
}

// doParse 主门解析（对齐 doParse 的 tree-sitter 分支）。
func doParse(command string) ParsedCommand {
	if command == "" {
		return nil
	}
	data := ParseCommand(command)
	if data == nil {
		return nil
	}
	return BuildParsedCommandFromRoot(command, data.RootNode)
}
