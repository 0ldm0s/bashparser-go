package parser

import "testing"

// TestGitIgnoreMatcherIgnoreV6Parity 与上游 npm ignore v6 的双端
// 实测基准对齐（node 侧同模式同路径实测，数据见 eva-go 权限域
// 覆盖基准文档批 2b 记录）。
func TestGitIgnoreMatcherIgnoreV6Parity(t *testing.T) {
	m := NewGitIgnoreMatcher([]string{
		"logs/**",
		"*.log",
		"!/keep.log",
		"/rooted.txt",
		"build",
	})
	cases := []struct {
		path    string
		ignored bool
		ruleIdx int
	}{
		{"logs/a.txt", true, 0},
		{"sub/x.log", true, 1},
		{"keep.log", false, 2}, // !/keep.log 取反（前缀无空白）
		{"sub/keep.log", true, 1},
		{"rooted.txt", true, 3},
		{"other/rooted.txt", false, -1},
		{"build/obj.o", true, 4},
		{"build", true, 4},
		{"normal.txt", false, -1},
	}
	for _, c := range cases {
		ignored, idx, _ := m.Test(c.path)
		if ignored != c.ignored {
			t.Errorf("Test(%q) ignored = %v, 期望 %v", c.path, ignored, c.ignored)
		}
		if ignored && idx != c.ruleIdx {
			t.Errorf("Test(%q) 规则下标 = %d, 期望 %d", c.path, idx, c.ruleIdx)
		}
	}
}

// TestGitIgnoreMatcherComplex 二轮复杂形态（与 ignore v6 实测对齐；
// 含尾斜杠目录语义——dir/ 不命中裸目录名）。
func TestGitIgnoreMatcherComplex(t *testing.T) {
	m := NewGitIgnoreMatcher([]string{
		"**/temp*/**",
		"*[0-9].txt",
		"!sub/*[0-9].txt",
		"dir/",
		"/top-only.txt",
		"a/**/b.txt",
	})
	cases := []struct {
		path    string
		ignored bool
	}{
		{"deep/temp/x", true},
		{"temp", false},      // **/temp*/** 不命中裸名（**/ 可零层但尾 /** 需内部）
		{"a/5.txt", true},
		{"sub/5.txt", false}, // !sub/*[0-9].txt 取反
		{"dir", false},       // dir/ 尾斜杠目录模式不命中裸目录名
		{"dir/f", true},      // 目录内部命中
		{"top-only.txt", true},
		{"sub/top-only.txt", false},
		{"a/x/y/b.txt", true},
		{"a/b.txt", true},
	}
	for _, c := range cases {
		if ignored, _, _ := m.Test(c.path); ignored != c.ignored {
			t.Errorf("Test(%q) ignored = %v, 期望 %v", c.path, ignored, c.ignored)
		}
	}
}
