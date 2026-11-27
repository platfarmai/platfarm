package main

import (
	"bytes"
	_ "embed"
	"fmt"
	"html"
	"regexp"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"
)

// payGuideMD 与 isvGuideMD 是仓库 docs/ 下两份指南的副本：go:embed 够不到模块目录之外，
// 镜像构建上下文也只有本目录。两份是否一致由 TestGuideCopyMatchesRepoDoc 把关。
//
//go:embed content/pay-open-api.zh-CN.md
var payGuideMD []byte

//go:embed content/pay-isv-guide.zh-CN.md
var isvGuideMD []byte

type tocEntry struct {
	Level int
	ID    string
	Title string
}

type guide struct {
	Title string
	Body  string // 渲染后的 HTML 片段
	TOC   []tocEntry
}

var sectionNumRe = regexp.MustCompile(`^\d+(\.\d+)*`)

// renderGuide 把接入指南渲染成 HTML。二、三级标题带上稳定锚点：
// "2.1 平台 app token" → #s-2-1，没有编号的标题按出现顺序编号。
// 原始 HTML 不透传（goldmark 默认行为），所以文档里的 HTML 注释不会出现在页面上。
func renderGuide(src []byte) (guide, error) {
	src = bytes.ReplaceAll(src, []byte("\r\n"), []byte("\n"))
	md := goldmark.New(goldmark.WithExtensions(extension.Table))
	doc := md.Parser().Parse(text.NewReader(src))

	var g guide
	used := map[string]bool{}
	seq := 0
	err := ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		h, ok := n.(*ast.Heading)
		if !ok || !entering {
			return ast.WalkContinue, nil
		}
		title := strings.TrimSpace(nodeText(h, src))
		if h.Level == 1 && g.Title == "" {
			g.Title = title
		}
		if h.Level == 2 || h.Level == 3 {
			seq++
			id := fmt.Sprintf("s-h%d", seq)
			if num := sectionNumRe.FindString(title); num != "" {
				id = "s-" + strings.ReplaceAll(num, ".", "-")
			}
			if used[id] {
				id = fmt.Sprintf("%s-h%d", id, seq)
			}
			used[id] = true
			h.SetAttributeString("id", []byte(id))
			g.TOC = append(g.TOC, tocEntry{Level: h.Level, ID: id, Title: title})
		}
		return ast.WalkSkipChildren, nil
	})
	if err != nil {
		return g, err
	}

	var buf bytes.Buffer
	if err := md.Renderer().Render(&buf, src, doc); err != nil {
		return g, err
	}
	// 表格可能比正文宽（接口表、wayCode 表），包一层横向滚动容器。
	// 原始 HTML 不透传，所以这里的 <table> 只可能来自渲染器。
	body := strings.ReplaceAll(buf.String(), "<table>", `<div class="site-table-wrap"><table>`)
	g.Body = strings.ReplaceAll(body, "</table>", "</table></div>")
	return g, nil
}

func nodeText(n ast.Node, src []byte) string {
	var b strings.Builder
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		switch t := c.(type) {
		case *ast.Text:
			b.Write(t.Segment.Value(src))
		case *ast.String:
			b.Write(t.Value)
		default:
			b.WriteString(nodeText(c, src))
		}
	}
	return b.String()
}

type docMeta struct {
	Subtitle   string
	MetaDesc   string
	BannerHTML string
	FooterNote string
}

// docsPage 把渲染结果填进 static/docs.html 模板。
func docsPage(tmpl []byte, g guide, m docMeta) []byte {
	var toc strings.Builder
	for _, e := range g.TOC {
		class := "site-toc-item"
		if e.Level == 3 {
			class += " site-toc-sub"
		}
		fmt.Fprintf(&toc, `<a class="%s" href="#%s">%s</a>`+"\n", class, e.ID, html.EscapeString(e.Title))
	}
	title := g.Title
	if title == "" {
		title = "接入指南"
	}
	return []byte(strings.NewReplacer(
		"{{TITLE}}", html.EscapeString(title),
		"{{SUBTITLE}}", html.EscapeString(m.Subtitle),
		"{{META_DESC}}", html.EscapeString(m.MetaDesc),
		"{{BANNER}}", m.BannerHTML,
		"{{FOOTER_NOTE}}", m.FooterNote,
		"{{TOC}}", toc.String(),
		"{{BODY}}", g.Body,
	).Replace(string(tmpl)))
}
