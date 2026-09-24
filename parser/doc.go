// Package parser：eva-cli bashParser.ts（纯 TS bash 解析器）的 Go 1:1 直译。
//
// 对拍纪律（用户 2026-09-24 裁定）：
//   - 全量 1:1，不允许切片——上游 bashParser.ts 的每一个函数、每一个
//     分支都必须有对应 Go 实现，缺一处即未完成
//   - 验收 = 同输入两侧 AST 字节级一致（工具原始输出为证）
//   - 对拍集必须覆盖全部语法形态（基础/复合/对抗/bail 路径/非 BMP 字符）
//
// 直译对照（逐文件）：
//   lexer.go    ← bashParser.ts 48-591（Tokenizer）
//   state.go    ← bashParser.ts 593-704（ParseState/mk/sliceBytes/预算）
//   statements.go   ← 706-992（program/statements/and_or/pipeline）
//   command.go      ← 995-1623（command/simple_command/assignment/subscript）
//   redirect.go     ← 1588-2001（redirect/process_sub/heredoc 体扫描）
//   word.go         ← 2003-2553（word/bare_word/double_quoted/dollar_like）
//   brace.go        ← 2215-2337（brace_expression/brace_like_cat）
//   expansion.go    ← 2555-3150（expansion_body/rest/regex_segmented/backtick）
//   control.go      ← 3150-3700（if/while/for/case/function/declaration/unset）
//   testexpr.go     ← 3699-4128（test 表达式全家族）
//   arith.go        ← 4129-4437（算术表达式全家族）
//   行号为建立时上游（eva-cli cf2422c9 附近）的 bashParser.ts 行号锚点。
package parser
