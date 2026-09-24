# bashparser-go

bash 解析与安全分析 Go 库——eva-cli `utils/bash` 包（纯 TS bash 解析栈）
的**全量 1:1 Go 直译**，产出 tree-sitter-bash 兼容 AST
（`TsNode`：type/text/startIndex/endIndex/children，字节偏移为 UTF-8）
并在其上提供完整的安全分析能力。

本库为正式交付物（非 POC），后续 eva-go 的 Bash 工具移植直接依赖它。

## 覆盖范围（上游 utils/bash 五模块）

| 上游文件 | 行数 | 本库对应 | 入口 |
|---|---|---|---|
| bashParser.ts | 4437 | parser/ 19 文件（v0.1.0） | `ParseSource` |
| parser.ts | 230 | gate.go | `ParseCommand`/`ParseCommandRaw`（三态）/`FindCommandNode` 等 |
| ast.ts | 2680 | ast_*.go 七文件 | `ParseForSecurity`/`ParseForSecurityFromAst`/`CheckSemantics` |
| treeSitterAnalysis.ts | 507 | tree_sitter_analysis*.go | `AnalyzeCommand`/`ExtractQuoteContext` 等 |
| ParsedCommand.ts | 319 | parsed_command.go | `Parse`/`BuildParsedCommandFromRoot` |

上游解析栈共三层：bashParser.ts（核心解析器，本库覆盖）→ parser.ts
（外层封装，含 10000 字符输入长度门）→ ast.ts（安全判定）。tree-sitter
原生模块的加载探测/功能门控在 Go 侧不存在——库静态链接恒可用。

## 用法

```go
import "github.com/0ldm0s/bashparser-go/parser"

// 安全解析：三态（simple / too-complex / parse-unavailable）
result := parser.ParseForSecurity("echo hello | wc -c")
// result.Kind == "simple"；result.Commands 为扁平命令列表（引号已解析）
// too-complex 时 Reason/NodeType 说明原因（调用方应 fail-closed）

// 语义检查（Post-argv：eval-like/zsh 内建/数组下标算术/jq/proc 等）
sem := parser.CheckSemantics(result.Commands)

// 原始解析与命令级视图
root, st := parser.ParseCommandRaw("echo hi > out.txt", 0)
pc := parser.Parse("echo a | b && c | d")  // ParsedCommand：管道分段/重定向剥离
ana := parser.AnalyzeCommand(root, cmd)    // 引号上下文/复合结构/危险模式
```

库为纯 Go、零第三方依赖。上游 50ms 超时 + 5 万节点预算的
防炸语义原样保留（病理输入返回 nil → 上层 fail-closed）。

## 结构

- `parser/`——库本体（bashParser.ts 19 文件 + gate + ast 七文件 +
  tree_sitter_analysis + parsed_command，均 ≤500 行，对照上游逐段
  直译，文件头注明行号锚点）
- `cmd/dump/`——AST/token dump 工具（互拍验证入口）
- `fixtures/`——对拍输入与两侧基准产物（137 条 AST 互拍 +
  28 条分析互拍 + 79 条 ast 对拍）

## 互拍验证（一致性证据）

```bash
# Go 侧
go run ./cmd/dump -mode ast -in fixtures/commands.jsonl -out fixtures/go-ast.jsonl

# TS 侧（在 eva-cli 作用域内，dump.mjs import 上游 bashParser.ts 原版）
cp ts/dump.mjs /path/to/eva-cli/ts-poc-dump.mjs
cd /path/to/eva-cli
npx tsx ts-poc-dump.mjs /path/to/fixtures/commands.jsonl /path/to/ts-ast.jsonl

# 比对（当前基线：双侧 SHA256
# fd5a8d41b8c62cf2e0eeac4a88577eceb173300a23c963d06977853b6f933d82）
git diff --no-index fixtures/go-ast.jsonl fixtures/ts-ast.jsonl
```

### 上游黄金语料不可得

上游 `bashParser.ts` 头注释提到 3449 条黄金语料（generated from the
WASM parser），但该语料**不在 eva-cli 仓库中**（已验证：tests/ 全目录
无解析器语料文件），无法获取。一致性证据以自建对拍集为准。

### 对拍集边界

对拍集为人工设计的全语法形态**采样**，非全输入空间验证。
bash 语法组合空间无限，未测输入（JS/Go 正则语义差异、Unicode 边角、
数值边界等语言级差异）存在两侧行为不一致的理论可能。发现新的分歧
输入时：追加到 `fixtures/commands.jsonl`，重跑两侧 dump，更新 SHA256
基线，并登记到"已知运行时差异"。

### 已知运行时差异（登记）

1. 深递归输入（数千层 `$((` 嵌套）：V8 栈溢出 → catch → null；
   Go 栈弹性增长可完成解析。安全方向 Go 更完整。
2. 上游词法快照打包 `(b<<16)|i` 在输入超 65535 字符时溢出；
   本库用结构体快照，无此限制。
3. ast.ts 的 extractQuoteContext 逐字符遍历用 JS UTF-16 码元索引比较
   UTF-8 字节偏移 span——多字节命令下上游 span 与字符索引错位；
   Go 侧 string 索引即字节偏移，多字节场景天然正确（Go 修正）。
4. 解析中止（PARSE_TIMEOUT 50ms）触发阈值：上游 V8/WASM 约 2800 下标
   即中止；Go 原生速度下 10K 长度门内输入轻松完成——Go 更完整。

来源与设计文档：eva-go 仓库 `docs/bash解析器移植与互拍方案.md`、
`docs/权限域侦查/`。

## 许可

LGPL-3.0，详见 [LICENSE](LICENSE)。
