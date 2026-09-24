// TS 侧互拍 dump：读取 JSONL 输入（{"input": "..."} 每行），调用上游
// bashParser.ts 的 parseSource（经 getParserModule），输出
// {"input", "ast"} JSONL。
// 用法（在 eva-cli 作用域内）：npx tsx dump.mjs <input.jsonl> <output.jsonl>
import { readFileSync, writeFileSync } from 'fs'
import { getParserModule } from './src/utils/bash/bashParser.ts'

const [inputPath, outputPath] = process.argv.slice(2)
if (!inputPath || !outputPath) {
  console.error('用法: tsx dump.mjs <input.jsonl> <output.jsonl>')
  process.exit(1)
}
const mod = getParserModule()
if (!mod) {
  console.error('解析器模块初始化失败')
  process.exit(1)
}
const lines = readFileSync(inputPath, 'utf8').split('\n').filter(l => l.trim() !== '')
const outLines = lines.map(line => {
  const { input } = JSON.parse(line)
  const ast = mod.parse(input)
  return JSON.stringify({ input, ast })
})
writeFileSync(outputPath, outLines.join('\n') + '\n')
console.error(`TS dump 完成：${outLines.length} 条`)
