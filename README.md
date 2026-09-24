# bashparser-go

bash 解析器 Go 库——eva-cli `bashParser.ts`（纯 TS 手写 bash 解析器）的
**全量 1:1 Go 直译**，产出 tree-sitter-bash 兼容 AST
（`TsNode`：type/text/startIndex/endIndex/children，字节偏移为 UTF-8）。

与上游的一致性经**互拍验证**：同输入两侧 AST 输出逐字节一致
（137 条全语法形态对拍集，双侧 SHA256 全同）。

本库为正式交付物（非 POC），后续 eva-go 的 Bash 工具移植直接依赖它。

## 覆盖范围

上游解析栈共三层：

- `bashParser.ts`（4437 行）——核心 bash 解析器 → **本库覆盖这一层**
- `parser.ts`（230 行）——外层封装，含 10000 字符输入长度门
- `ast.ts`（2680 行）——AST 安全判定

`parser.ts` 与 `ast.ts` 的 Go 移植不在本库范围
（后续工作，见 eva-go 落地计划 C6c）。

## 用法

```go
import "github.com/0ldm0s/bashparser-go/parser"

root := parser.ParseSource("echo hello | wc -c", 0) // 超时 ms，0 = 默认 50ms
// root.Type == "program"；解析中止/超时返回 nil
// 遍历 root.Children / node.Type / node.Text / node.StartIndex / node.EndIndex
```

库为纯 Go、零第三方依赖。上游 50ms 超时 + 5 万节点预算的
防炸语义原样保留（病理输入返回 nil）。

## 结构

- `parser/`——库本体（token/lexer/state/statements/command/assignment/
  redirect/heredoc/word/quoted/brace/expansion/expansion_rest/control/
  control_func/testexpr/arith，19 文件均 ≤500 行，对照上游 bashParser.ts
  4437 行逐段直译，文件头注明行号锚点）
- `cmd/dump/`——AST/token dump 工具（互拍验证入口）
- `fixtures/`——137 条全语法形态对拍输入 + 两侧基准产物
- `ts/dump.mjs`——TS 侧基准生成脚本（在 eva-cli 作用域内运行）

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
无解析器语料文件），无法获取。一致性证据以自建 137 条对拍集为准。

### 对拍集边界

137 条对拍集为人工设计的全语法形态**采样**，非全输入空间验证。
bash 语法组合空间无限，未测输入（JS/Go 正则语义差异、Unicode 边角、
数值边界等语言级差异）存在两侧行为不一致的理论可能。发现新的分歧
输入时：追加到 `fixtures/commands.jsonl`，重跑两侧 dump，更新 SHA256
基线，并登记到"已知运行时差异"。

## 已知运行时差异（登记）

1. 深递归输入（数千层 `$((` 嵌套）：V8 栈溢出 → catch → null；
   Go 栈弹性增长可完成解析。安全方向 Go 更完整。
2. 上游词法快照打包 `(b<<16)|i` 在输入超 65535 字符时溢出；
   本库用结构体快照，无此限制。

来源与设计文档：eva-go 仓库 `docs/bash解析器移植与互拍方案.md`。

## 许可

LGPL-3.0，详见 [LICENSE](LICENSE)。
