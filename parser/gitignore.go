// gitignore.go——最小 gitignore 语义匹配器（上游 eva-cli 消费的
// npm `ignore` 库 v6 的 Go 等价物，行为以双端实测对齐——见
// eva-go 权限域覆盖基准文档的对拍记录）。
//
// 覆盖语义（逐形态与 ignore v6 实测对齐）：
//   - 后模式覆盖前模式（最后命中生效）
//   - `!` 取反（ignored=false 但仍算命中规则——供规则回溯）
//   - `/` 锚定（剥前导 / 后全串锚定；不命中子目录相对路径）
//   - `**/` 任意层级前缀（可零层）、中段 `**` 跨层、尾 `/**` 自身
//     与内部
//   - `*` 单层内任意、`?` 单字符、`[...]` 字符类
//   - 尾 `/` 目录模式：仅匹配目录内部（dir/ 命中 dir/f 不命中裸
//     dir——与 ignore v6 实测一致；本实现曾按 basename 锚定误拦
//     裸目录名，2026-09-29 对拍修正）
//   - 无尾斜杠 basename 型（不含 /）：匹配任意层级的同名目录段
//     及其内部（build 命中 build 与 build/obj.o）
//
// 边界：不实现 gitignore 全部角标语义（如 `/**/` 零层差异的极端
// 形态）；权限规则形态未使用到——扩展需同步补对拍。

package parser

import (
	"regexp"
	"strings"
)

// GitIgnoreMatcher gitignore 语义匹配器（规则保序，后模式覆盖
// 前模式）。
type GitIgnoreMatcher struct {
	patterns []gitignorePattern
}

type gitignorePattern struct {
	raw    string // 原始模式（回溯用）
	negate bool   // ! 取反
	re     *regexp.Regexp
}

// NewGitIgnoreMatcher 构建匹配器（空串模式跳过）。
func NewGitIgnoreMatcher(patterns []string) *GitIgnoreMatcher {
	m := &GitIgnoreMatcher{}
	for _, p := range patterns {
		if p == "" {
			continue
		}
		negate := false
		if strings.HasPrefix(p, "!") {
			negate = true
			p = p[1:]
		}
		m.patterns = append(m.patterns, gitignorePattern{
			raw:    p,
			negate: negate,
			re:     regexp.MustCompile(gitignorePatternToRegex(p)),
		})
	}
	return m
}

// Test 返回 (是否忽略, 命中规则下标, 命中模式原文)；无命中返回
// (false, -1, "")。
//
// 逐级判定语义（对齐 ignore v6 checkUnignored 的递归逐级）：对路径
// 各前缀从浅到深跑「最后命中」——某级命中非取反 → 该级及其内部全部
// 忽略（父目录被排除后内部取反无效，git 规范）；命中取反 → 该级未
// 忽略继续下探。原文供调用方回溯映射到更高层规则对象。
func (m *GitIgnoreMatcher) Test(relativePath string) (bool, int, string) {
	segments := strings.Split(relativePath, `/`)
	for level := 1; level <= len(segments); level++ {
		prefix := strings.Join(segments[:level], `/`)
		lastHit := -1
		for i, p := range m.patterns {
			if p.re.MatchString(prefix) {
				lastHit = i
			}
		}
		if lastHit >= 0 && !m.patterns[lastHit].negate {
			return true, lastHit, m.patterns[lastHit].raw
		}
	}
	return false, -1, ""
}

// gitignorePatternToRegex 模式 → 锚定正则（对齐 ignore v6 实测：
// 尾 / 目录模式仅匹配内部；锚定模式剥前导 /；basename 型补 **/
// 任意层级前缀）。
func gitignorePatternToRegex(pattern string) string {
	dirOnly := strings.HasSuffix(pattern, `/`)
	pattern = strings.TrimSuffix(pattern, `/`)

	anchored := strings.Contains(pattern, `/`)
	if strings.HasPrefix(pattern, `/`) {
		pattern = pattern[1:]
		anchored = true
	}

	if dirOnly {
		// 尾 / 目录模式：仅匹配目录内部（dir/ 对裸名 dir 不忽略、
		// 对 dir/f 忽略——ignore v6 实测；逐级判定下 target/keep 级
		// 被 target/ 忽略后 keep 内部取反无效）
		return `^` + pattern + `/.*$`
	}
	if !anchored {
		pattern = `**/` + pattern
	}

	var b strings.Builder
	b.WriteString(`^`)
	i := 0
	for i < len(pattern) {
		switch {
		// /**/ 整体（中段）：两斜杠归转换框架、** 产零或多层
		//（a/**/b.txt 命中 a/b.txt 与 a/x/y/b.txt——ignore v6 实测；
		// 须先于 /** 与 **/ 判定，否则字面 / 与 (?:.*/)? 各带一个
		// 斜杠产生 //）
		case strings.HasPrefix(pattern[i:], `/**/`):
			b.WriteString(`/(?:.*/)?`)
			i += 4
		// 尾 /**：内部任意（logs/** 命中 logs/a.txt）
		case strings.HasPrefix(pattern[i:], `/**`) && i+3 == len(pattern):
			b.WriteString(`/.*`)
			i += 3
		// 头 **/：任意层级前缀（**/temp*/** 的 temp 前可零或多层）
		case strings.HasPrefix(pattern[i:], `**/`):
			b.WriteString(`(?:.*/)?`)
			i += 3
		case pattern[i] == '*':
			b.WriteString(`[^/]*`)
			i++
		case pattern[i] == '?':
			b.WriteString(`[^/]`)
			i++
		case pattern[i] == '[':
			j := i + 1
			if j < len(pattern) && (pattern[j] == '^' || pattern[j] == '!') {
				j++
			}
			if j < len(pattern) && pattern[j] == ']' {
				j++
			}
			for j < len(pattern) && pattern[j] != ']' {
				j++
			}
			if j < len(pattern) {
				class := strings.ReplaceAll(pattern[i+1:j], `!`, `^`)
				b.WriteString(`[` + class + `]`)
				i = j + 1
			} else {
				b.WriteString(`\[`)
				i++
			}
		default:
			switch pattern[i] {
			case '.', '+', '$', '^', '(', ')', '{', '}', '|', '\\':
				b.WriteByte('\\')
			}
			b.WriteByte(pattern[i])
			i++
		}
	}
	b.WriteString(`$`)
	return b.String()
}
