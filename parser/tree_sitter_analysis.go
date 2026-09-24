// tree_sitter_analysis.go——上游 eva-cli utils/bash/treeSitterAnalysis.ts
// 30-64 行（类型）+ 292-507 行（extractCompoundStructure/
// hasActualOperatorNodes/extractDangerousPatterns/analyzeCommand）的 Go 直译。
package parser

// CompoundStructure 复合命令结构（对齐 treeSitterAnalysis.ts）。
type CompoundStructure struct {
	// HasCompoundOperators 顶层是否有复合算子（&&、||、;）
	HasCompoundOperators bool `json:"hasCompoundOperators"`
	// HasPipeline 是否有管道
	HasPipeline bool `json:"hasPipeline"`
	// HasSubshell 是否有子 shell
	HasSubshell bool `json:"hasSubshell"`
	// HasCommandGroup 是否有命令组（{...}）
	HasCommandGroup bool `json:"hasCommandGroup"`
	// Operators 顶层复合算子类型
	Operators []string `json:"operators"`
	// Segments 按复合算子切分的命令段
	Segments []string `json:"segments"`
}

// DangerousPatterns 危险模式（对齐 treeSitterAnalysis.ts）。
type DangerousPatterns struct {
	// HasCommandSubstitution 有 $() 或反引号命令替换
	HasCommandSubstitution bool `json:"hasCommandSubstitution"`
	// HasProcessSubstitution 有 <() 或 >() 进程替换
	HasProcessSubstitution bool `json:"hasProcessSubstitution"`
	// HasParameterExpansion 有 ${...} 参数展开
	HasParameterExpansion bool `json:"hasParameterExpansion"`
	// HasHeredoc 有 heredoc
	HasHeredoc bool `json:"hasHeredoc"`
	// HasComment 有注释
	HasComment bool `json:"hasComment"`
}

// Analysis 完整分析产物（对齐 treeSitterAnalysis.ts TreeSitterAnalysis）。
type Analysis struct {
	QuoteContext QuoteContext `json:"quoteContext"`
	// CompoundStructure 复合结构
	CompoundStructure CompoundStructure `json:"compoundStructure"`
	// HasActualOperatorNodes 存在真实算子节点（;、&&、||）——为 false 时
	// \; 只是 word 参数
	HasActualOperatorNodes bool `json:"hasActualOperatorNodes"`
	// DangerousPatterns 危险模式
	DangerousPatterns DangerousPatterns `json:"dangerousPatterns"`
}

// extractCompoundStructure 从 AST 提取复合命令结构（对齐
// extractCompoundStructure——替代 tree-sitter 路径的
// isUnsafeCompoundCommand/splitCommand）。
func extractCompoundStructure(rootNode *TsNode, command string) CompoundStructure {
	// 初始化为空切片（非 nil）——JSON 序列化为 []，与上游空数组一致
	operators := []string{}
	segments := []string{}
	hasSubshell := false
	hasCommandGroup := false
	hasPipeline := false

	// walkChildren 遍历子节点列表（对齐 walkTopLevel——上游以
	// {...node, children: [child]} 伪节点复用函数体，Go 直接传列表）。
	var walkChildren func(children []*TsNode)
	walkChildren = func(children []*TsNode) {
		for _, child := range children {
			if child == nil {
				continue
			}

			switch child.Type {
			case "list":
				// list 节点含 && 与 || 算子
				for _, listChild := range child.Children {
					if listChild == nil {
						continue
					}
					if listChild.Type == "&&" || listChild.Type == "||" {
						operators = append(operators, listChild.Type)
					} else if listChild.Type == "list" ||
						listChild.Type == "redirected_statement" {
						// 嵌套 list，或包裹 list/pipeline 的
						// redirected_statement——递归使内层算子/管道被检出。
						// `cmd1 && cmd2 2>/dev/null && cmd3` 中
						// redirected_statement 包着 list(cmd1 && cmd2)——
						// 不递归会漏内层 &&。
						walkChildren([]*TsNode{listChild})
					} else if listChild.Type == "pipeline" {
						hasPipeline = true
						segments = append(segments, listChild.Text)
					} else if listChild.Type == "subshell" {
						hasSubshell = true
						segments = append(segments, listChild.Text)
					} else if listChild.Type == "compound_statement" {
						hasCommandGroup = true
						segments = append(segments, listChild.Text)
					} else {
						segments = append(segments, listChild.Text)
					}
				}
			case ";":
				operators = append(operators, ";")
			case "pipeline":
				hasPipeline = true
				segments = append(segments, child.Text)
			case "subshell":
				hasSubshell = true
				segments = append(segments, child.Text)
			case "compound_statement":
				hasCommandGroup = true
				segments = append(segments, child.Text)
			case "command", "declaration_command", "variable_assignment":
				segments = append(segments, child.Text)
			case "redirected_statement":
				// `cd ~/src && find path 2>/dev/null`——tree-sitter 把整个
				// 复合包进 redirected_statement：program →
				// redirected_statement → (list → cmd1, &&, cmd2) +
				// file_redirect。`cmd1 | cmd2 > out`（包 pipeline）与
				// `(cmd) > out`（包 subshell）同理。递归检出内层结构；
				// 跳过 file_redirect 子（重定向不影响复合/管道分类）。
				foundInner := false
				for _, inner := range child.Children {
					if inner == nil || inner.Type == "file_redirect" {
						continue
					}
					foundInner = true
					walkChildren([]*TsNode{inner})
				}
				if !foundInner {
					// 无体的孤立重定向（不应出现，保守兜底）
					segments = append(segments, child.Text)
				}
			case "negated_command":
				// `! cmd`——递归内层使命令结构被分类（管道/子 shell 等），
				// 同时把完整取反文本记为段，保持 segments.length 有意义。
				segments = append(segments, child.Text)
				walkChildren(child.Children)
			case "if_statement", "while_statement", "for_statement",
				"case_statement", "function_definition":
				// 控制流结构：结构本身记为一段，同时递归使内部
				// 管道/子 shell/算子被检出。
				segments = append(segments, child.Text)
				walkChildren(child.Children)
			}
		}
	}

	walkChildren(rootNode.Children)

	// 无段时整条命令即一段
	if len(segments) == 0 {
		segments = append(segments, command)
	}

	return CompoundStructure{
		HasCompoundOperators: len(operators) > 0,
		HasPipeline:          hasPipeline,
		HasSubshell:          hasSubshell,
		HasCommandGroup:      hasCommandGroup,
		Operators:            operators,
		Segments:             segments,
	}
}

// hasActualOperatorNodes 检查 AST 是否含真实算子节点（;、&&、||）
// （对齐 hasActualOperatorNodes）。
//
// 这是消除 `find -exec \;` 误报的关键函数。tree-sitter 把 `\;` 解析为
// word 节点的一部分（find 的参数），不是 `;` 算子。AST 中无真实 `;`
// 算子节点即无复合算子，可跳过 hasBackslashEscapedOperator。
func hasActualOperatorNodes(rootNode *TsNode) bool {
	var walk func(node *TsNode) bool
	walk = func(node *TsNode) bool {
		// 指示复合命令的算子类型
		if node.Type == ";" || node.Type == "&&" || node.Type == "||" {
			return true
		}
		// list 节点即意味着有复合算子
		if node.Type == "list" {
			return true
		}
		for _, child := range node.Children {
			if child != nil && walk(child) {
				return true
			}
		}
		return false
	}
	return walk(rootNode)
}

// extractDangerousPatterns 从 AST 提取危险模式信息（对齐
// extractDangerousPatterns）。
func extractDangerousPatterns(rootNode *TsNode) DangerousPatterns {
	var p DangerousPatterns
	var walk func(node *TsNode)
	walk = func(node *TsNode) {
		switch node.Type {
		case "command_substitution":
			p.HasCommandSubstitution = true
		case "process_substitution":
			p.HasProcessSubstitution = true
		case "expansion":
			p.HasParameterExpansion = true
		case "heredoc_redirect":
			p.HasHeredoc = true
		case "comment":
			p.HasComment = true
		}
		for _, child := range node.Children {
			if child != nil {
				walk(child)
			}
		}
	}
	walk(rootNode)
	return p
}

// AnalyzeCommand 对命令做完整分析（对齐 analyzeCommand——单趟提取
// 全部安全相关数据；须在树释放前调用）。
func AnalyzeCommand(rootNode *TsNode, command string) Analysis {
	return Analysis{
		QuoteContext:           ExtractQuoteContext(rootNode, command),
		CompoundStructure:      extractCompoundStructure(rootNode, command),
		HasActualOperatorNodes: hasActualOperatorNodes(rootNode),
		DangerousPatterns:      extractDangerousPatterns(rootNode),
	}
}
