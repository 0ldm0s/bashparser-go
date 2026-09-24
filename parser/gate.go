// gate.go——上游 eva-cli utils/bash/parser.ts（封装层）的 Go 直译。
// 主门三态语义：Parsed / Aborted / Unavailable。上游 tree-sitter 原生
// 模块的加载探测/功能门控（feature('TREE_SITTER_BASH')、
// ensureParserInitialized）在 Go 侧不存在——库静态链接恒可用，相关
// 分支折叠。

package parser

// maxCommandLength 输入长度门（对齐 parser.ts MAX_COMMAND_LENGTH = 10000）。
// 超长返回不可用（可降级），不是中止。
const maxCommandLength = 10000

// GateState 主门解析结果状态（对齐 parseCommandRaw 的三态返回：
// Node | null | PARSE_ABORTED）。
type GateState int

const (
	// GateParsed 解析成功
	GateParsed GateState = iota
	// GateAborted 已尝试解析但中止（超时/节点预算/panic）——调用方必须
	// fail-closed（视为太复杂），不得路由到回退路径。对齐 parser.ts
	// PARSE_ABORTED 语义：折叠为降级曾导致 trap/enable/hash 判定泄漏。
	GateAborted
	// GateUnavailable 空命令/超长/主门不可用——调用方可降级回退路径。
	// Go 侧库静态链接恒可用，此态仅由空/超长触发。
	GateUnavailable
)

// ParsedCommandData 解析产物（对齐 parser.ts ParsedCommandData）。
type ParsedCommandData struct {
	RootNode        *TsNode
	EnvVars         []string
	CommandNode     *TsNode
	OriginalCommand string
}

// ParseCommandRaw 原始解析（对齐 parser.ts parseCommandRaw）。
// 跳过 ast.ts 安全遍历器不使用的 findCommandNode/extractEnvVars。
// 返回 (nil, GateUnavailable) = 空/超长；(nil, GateAborted) = 解析中止，
// 调用方必须 fail-closed。
func ParseCommandRaw(command string) (*TsNode, GateState) {
	if command == "" || len(command) > maxCommandLength {
		return nil, GateUnavailable
	}
	return parseCommandRawChecked(command, 0)
}

// parseCommandRawChecked 可调超时的解析入口（timeoutMs 0 = 默认 50ms）。
// 供测试收紧预算验证 Aborted 分支；生产路径走 ParseCommandRaw。
func parseCommandRawChecked(command string, timeoutMs int) (*TsNode, GateState) {
	root := ParseSource(command, timeoutMs)
	if root == nil {
		// 安全：解析器已加载；nil = 超时/节点预算中止
		//（PARSE_TIMEOUT_MS=50，MAX_NODES=50_000）
		return nil, GateAborted
	}
	return root, GateParsed
}

// ParseCommand 完整解析（对齐 parser.ts parseCommand）。
// 附带 commandNode/envVars 提取；中止/不可用折叠为 nil（对齐上游
// parseCommand 的 catch → null——与 ParseCommandRaw 的三态不同）。
func ParseCommand(command string) *ParsedCommandData {
	if command == "" || len(command) > maxCommandLength {
		return nil
	}
	root := ParseSource(command, 0)
	if root == nil {
		return nil
	}
	commandNode := findCommandNode(root, nil)
	return &ParsedCommandData{
		RootNode:        root,
		EnvVars:         ExtractEnvVars(commandNode),
		CommandNode:     commandNode,
		OriginalCommand: command,
	}
}

// commandTypes 命令节点类型（对齐 parser.ts COMMAND_TYPES）
var commandTypes = map[string]bool{
	"command":             true,
	"declaration_command": true,
}

// findCommandNode 查找首个命令节点（对齐 parser.ts findCommandNode）。
func findCommandNode(node *TsNode, parent *TsNode) *TsNode {
	if commandTypes[node.Type] {
		return node
	}

	// 变量赋值后跟命令
	if node.Type == "variable_assignment" && parent != nil {
		for _, c := range parent.Children {
			if commandTypes[c.Type] && c.StartIndex > node.StartIndex {
				return c
			}
		}
		return nil
	}

	// 管道：递归到第一个子节点（可能是 redirected_statement）
	if node.Type == "pipeline" {
		for _, child := range node.Children {
			if r := findCommandNode(child, node); r != nil {
				return r
			}
		}
		return nil
	}

	// 重定向语句：查找其中的命令
	if node.Type == "redirected_statement" {
		for _, c := range node.Children {
			if commandTypes[c.Type] {
				return c
			}
		}
		return nil
	}

	// 递归搜索
	for _, child := range node.Children {
		if r := findCommandNode(child, node); r != nil {
			return r
		}
	}
	return nil
}

// ExtractEnvVars 提取命令前缀环境变量赋值（对齐 parser.ts extractEnvVars：
// command 节点下收集 variable_assignment，遇 command_name/word 停）。
func ExtractEnvVars(commandNode *TsNode) []string {
	if commandNode == nil || commandNode.Type != "command" {
		return nil
	}
	var envVars []string
	for _, child := range commandNode.Children {
		if child.Type == "variable_assignment" {
			envVars = append(envVars, child.Text)
		} else if child.Type == "command_name" || child.Type == "word" {
			break
		}
	}
	return envVars
}

// declarationCommands 声明命令集合（对齐 DECLARATION_COMMANDS）
var declarationCommands = map[string]bool{
	"export": true, "declare": true, "typeset": true, "readonly": true,
	"local": true, "unset": true, "unsetenv": true,
}

// argumentTypes 参数节点类型（对齐 ARGUMENT_TYPES）
var argumentTypes = map[string]bool{
	"word": true, "string": true, "raw_string": true, "number": true,
}

// substitutionTypes 遇到即停的替换类型（对齐 SUBSTITUTION_TYPES）
var substitutionTypes = map[string]bool{
	"command_substitution": true, "process_substitution": true,
}

// ExtractCommandArguments 提取命令名与参数（对齐 parser.ts
// extractCommandArguments：引号剥离，遇替换节点停）。
func ExtractCommandArguments(commandNode *TsNode) []string {
	// 声明命令
	if commandNode.Type == "declaration_command" {
		if len(commandNode.Children) > 0 {
			first := commandNode.Children[0]
			if declarationCommands[first.Text] {
				return []string{first.Text}
			}
		}
		return nil
	}

	var args []string
	foundCommandName := false

	for _, child := range commandNode.Children {
		if child.Type == "variable_assignment" {
			continue
		}

		// 命令名
		if child.Type == "command_name" ||
			(!foundCommandName && child.Type == "word") {
			foundCommandName = true
			args = append(args, child.Text)
			continue
		}

		// 参数
		if argumentTypes[child.Type] {
			args = append(args, stripQuotes(child.Text))
		} else if substitutionTypes[child.Type] {
			break
		}
	}
	return args
}

// stripQuotes 去除成对首尾引号（对齐 parser.ts stripQuotes）。
func stripQuotes(text string) string {
	if len(text) >= 2 &&
		((text[0] == '"' && text[len(text)-1] == '"') ||
			(text[0] == '\'' && text[len(text)-1] == '\'')) {
		return text[1 : len(text)-1]
	}
	return text
}
