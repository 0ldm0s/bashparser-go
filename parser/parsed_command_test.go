package parser

import (
	"reflect"
	"testing"
)

// TestPipeSegmentsOutOfOrder 管道分段——乱序位置排序
// （对齐 extractPipePositions 注释：`a | b && c | d` 外层 | 先于内层 | 被访问。
// 期望 3 段：两个 | 之间是 "b && c"（无管道算子，&& 不切分））
func TestPipeSegmentsOutOfOrder(t *testing.T) {
	pc := Parse("a | b && c | d")
	if pc == nil {
		t.Fatal("解析失败")
	}
	got := pc.PipeSegments()
	want := []string{"a", "b && c", "d"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("应得 %v，得 %v", want, got)
	}
}

// TestPipeSegmentsQuotedPipe 引号内 | 不分段（主门 quote-aware 的价值）
func TestPipeSegmentsQuotedPipe(t *testing.T) {
	pc := Parse(`echo 'a | b'`)
	if pc == nil {
		t.Fatal("解析失败")
	}
	got := pc.PipeSegments()
	if len(got) != 1 {
		t.Errorf("引号内管道不分段，应 1 段，得 %v", got)
	}
}

// TestWithoutOutputRedirections 输出重定向剥离
func TestWithoutOutputRedirections(t *testing.T) {
	cases := []struct {
		cmd, want string
	}{
		{"echo hi > out.txt", "echo hi"},
		{"echo hi >> out.txt", "echo hi"},
		// tree-sitter-bash 的 file_redirect 吞掉重定向后的全部 word
		//（> out.txt extra 为一个节点）——整段切除后 extra 随之消失，
		// 与上游按 node.endIndex 切除的行为一致（已用 dump 验证 AST）
		{"echo hi > out.txt extra", "echo hi"},
		{"cmd > a.txt && cmd2 >> b.txt", "cmd && cmd2"},
		{"echo no-redirect", "echo no-redirect"},
	}
	for _, c := range cases {
		pc := Parse(c.cmd)
		if pc == nil {
			t.Fatalf("%q 解析失败", c.cmd)
		}
		if got := pc.WithoutOutputRedirections(); got != c.want {
			t.Errorf("%q 剥离应得 %q，得 %q", c.cmd, c.want, got)
		}
	}
}

// TestWithoutOutputRedirectionsMultibyte 多字节命令的字节偏移正确性
// （对齐 TreeSitterParsedCommand 注释：UTF-8 字节偏移切片）
func TestWithoutOutputRedirectionsMultibyte(t *testing.T) {
	pc := Parse(`echo 中文内容 > f.txt`)
	if pc == nil {
		t.Fatal("解析失败")
	}
	want := "echo 中文内容"
	if got := pc.WithoutOutputRedirections(); got != want {
		t.Errorf("多字节剥离应得 %q，得 %q", want, got)
	}
	if segs := pc.PipeSegments(); len(segs) != 1 || segs[0] == "" {
		t.Errorf("多字节无管道应 1 段，得 %v", segs)
	}
}

// TestOutputRedirectionsList 重定向清单
func TestOutputRedirectionsList(t *testing.T) {
	pc := Parse("cmd > a.txt && cmd2 >> b.txt")
	if pc == nil {
		t.Fatal("解析失败")
	}
	got := pc.OutputRedirections()
	if len(got) != 2 {
		t.Fatalf("应得 2 条重定向，得 %v", got)
	}
	if got[0].Target != "a.txt" || got[0].Operator != ">" {
		t.Errorf("第 1 条应为 a.txt/>>，得 %+v", got[0])
	}
	if got[1].Target != "b.txt" || got[1].Operator != ">>" {
		t.Errorf("第 2 条应为 b.txt/>>，得 %+v", got[1])
	}
}

// TestParseSingleCache 单项缓存（对齐 size-1 memoize：同命令返回同实例）
func TestParseSingleCache(t *testing.T) {
	a := Parse("echo cache-test")
	b := Parse("echo cache-test")
	if a == nil || a != b {
		t.Error("同命令重复 Parse 应命中缓存返回同实例")
	}
	c := Parse("echo other")
	if c == nil || c == a {
		t.Error("不同命令应重新解析")
	}
}

// TestCompoundStructureRedirectedWrapping 复合结构——redirected_statement
// 穿透（对齐 extractCompoundStructure 注释：`cmd1 && cmd2 2>/dev/null && cmd3`
// 整体被包裹，不递归会漏内层 &&）
func TestCompoundStructureRedirectedWrapping(t *testing.T) {
	pc := Parse("cmd1 && cmd2 2>/dev/null && cmd3")
	if pc == nil {
		t.Fatal("解析失败")
	}
	cs := pc.TreeSitterAnalysis().CompoundStructure
	if !cs.HasCompoundOperators {
		t.Error("应检出复合算子")
	}
	if len(cs.Operators) != 2 || cs.Operators[0] != "&&" || cs.Operators[1] != "&&" {
		t.Errorf("应检出 2 个 &&，得 %v", cs.Operators)
	}
	if len(cs.Segments) != 3 {
		t.Errorf("应 3 段，得 %v", cs.Segments)
	}
}

// TestCompoundStructureClassify 子 shell/命令组/管道分类
func TestCompoundStructureClassify(t *testing.T) {
	if pc := Parse("(echo sub)"); pc == nil || !pc.TreeSitterAnalysis().CompoundStructure.HasSubshell {
		t.Error("(cmd) 应检出 hasSubshell")
	}
	if pc := Parse("{ echo grp; }"); pc == nil || !pc.TreeSitterAnalysis().CompoundStructure.HasCommandGroup {
		t.Error("{ cmd; } 应检出 hasCommandGroup")
	}
	if pc := Parse("a | b"); pc == nil || !pc.TreeSitterAnalysis().CompoundStructure.HasPipeline {
		t.Error("a | b 应检出 hasPipeline")
	}
}

// TestHasActualOperatorNodesEscapedSemicolon `\;` 不是算子（对齐
// hasActualOperatorNodes 注释：消除 find -exec \; 误报的关键）
func TestHasActualOperatorNodesEscapedSemicolon(t *testing.T) {
	pc := Parse("find . -exec ls \\;")
	if pc == nil {
		t.Fatal("解析失败")
	}
	if pc.TreeSitterAnalysis().HasActualOperatorNodes {
		t.Error("\\; 是 word 参数，不应检出算子节点")
	}
	if pc2 := Parse("a && b"); pc2 == nil || !pc2.TreeSitterAnalysis().HasActualOperatorNodes {
		t.Error("a && b 应检出算子节点")
	}
}

// TestQuoteContextBasics 引号上下文（对齐 extractQuoteContext 三变体语义）
func TestQuoteContextBasics(t *testing.T) {
	qc := Parse("echo \"a b\" 'c'").TreeSitterAnalysis().QuoteContext

	// WithDoubleQuotes：双引号内容保留、定界符去除；单引号内容全去
	want := "echo a b "
	if qc.WithDoubleQuotes != want {
		t.Errorf("WithDoubleQuotes 应 %q，得 %q", want, qc.WithDoubleQuotes)
	}
	// FullyUnquoted：全部引号内容移除
	wantFully := "echo  "
	if qc.FullyUnquoted != wantFully {
		t.Errorf("FullyUnquoted 应 %q，得 %q", wantFully, qc.FullyUnquoted)
	}
	// UnquotedKeepQuoteChars：内容去、定界符留
	wantKeep := "echo \"\" ''"
	if qc.UnquotedKeepQuoteChars != wantKeep {
		t.Errorf("UnquotedKeepQuoteChars 应 %q，得 %q", wantKeep, qc.UnquotedKeepQuoteChars)
	}
}

// TestQuoteContextAnsiC ANSI-C 引用保留前导 $
func TestQuoteContextAnsiC(t *testing.T) {
	qc := Parse("echo $'x'").TreeSitterAnalysis().QuoteContext
	if !contains(qc.UnquotedKeepQuoteChars, "$'") {
		t.Errorf("ansi_c 应保留 $' 前导，得 %q", qc.UnquotedKeepQuoteChars)
	}
}

// TestQuoteContextQuotedHeredoc 引号 heredoc 剥离、非引号保留
// （对齐 quotedOnly 语义：非引号 heredoc 内 ${} 会被展开，校验器需看到）
func TestQuoteContextQuotedHeredoc(t *testing.T) {
	quoted := Parse("cat <<'EOF'\n${DANGER}\nEOF").TreeSitterAnalysis().QuoteContext
	if contains(quoted.FullyUnquoted, "${DANGER}") {
		t.Errorf("引号 heredoc 体应被剥离，得 %q", quoted.FullyUnquoted)
	}

	unquoted := Parse("cat <<EOF\n${DANGER}\nEOF").TreeSitterAnalysis().QuoteContext
	if !contains(unquoted.FullyUnquoted, "${DANGER}") {
		t.Errorf("非引号 heredoc 体应保留（bash 会展开），得 %q", unquoted.FullyUnquoted)
	}
}

// TestDangerousPatterns 危险模式检出
func TestDangerousPatterns(t *testing.T) {
	cases := []struct {
		cmd   string
		check func(DangerousPatterns) bool
	}{
		{"echo $(cmd)", func(p DangerousPatterns) bool { return p.HasCommandSubstitution }},
		{"diff <(a) b", func(p DangerousPatterns) bool { return p.HasProcessSubstitution }},
		{"echo ${VAR}", func(p DangerousPatterns) bool { return p.HasParameterExpansion }},
		{"cat <<EOF\nx\nEOF", func(p DangerousPatterns) bool { return p.HasHeredoc }},
		{"echo hi # note", func(p DangerousPatterns) bool { return p.HasComment }},
	}
	for i, c := range cases {
		pc := Parse(c.cmd)
		if pc == nil {
			t.Fatalf("case %d 解析失败", i)
		}
		if !c.check(pc.TreeSitterAnalysis().DangerousPatterns) {
			t.Errorf("case %d (%q) 危险模式未检出", i, c.cmd)
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && stringsContains(s, sub)
}

func stringsContains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
