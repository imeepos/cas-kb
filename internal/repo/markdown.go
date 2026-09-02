package repo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/imeepos/cas-kb/internal/hash"
	"github.com/imeepos/cas-kb/internal/object"
)

// Markdown 互操作(T29):知识库与编辑器/Obsidian 之间的文件树镜像。
// 设计要点见 DESIGN §6.8:
//   - 文件 = front-matter + 正文原文字节(逐字节保真,不增删换行);
//   - 导入走 BulkImport 等价路径(一次提交 + 一次索引增量);
//   - roundtrip 契约:export(import(X)) 与 X 逐字节一致;
//     import(export(库)) 写回后 diff 零变更(地址不变)。

// MdNote 是一篇笔记的 Markdown 互操作表示:
// 条目全路径 + front-matter 字段(title/tags)+ 正文原文字节。
type MdNote struct {
	Path  string
	Title string
	Tags  []string
	Body  string
}

// MdImportResult 描述一次 Markdown 导入。
type MdImportResult struct {
	// Snapshot 是本次导入产生的新快照;全部条目与当前一致时为空(无新快照)。
	Snapshot hash.Address
	// Imported 是实际写入(或还原)的条数。
	Imported int
	// Unchanged 是与当前条目逐字段一致而跳过的条数(地址不变)。
	Unchanged int
}

const (
	mdFence      = "---\n"
	mdTitleKey   = "title: "
	mdTagsKey    = "tags: "
	mdTagsJoined = ", "
)

// EncodeMdNote 把一篇笔记编码为 Markdown 文件字节:
// 首行 ---、第二行 title、有标签时一行 tags(逗号+空格分隔,无标签省略)、
// 再一行 ---,其后为正文原文字节。
func EncodeMdNote(n MdNote) []byte {
	var b bytes.Buffer
	b.WriteString(mdFence)
	b.WriteString(mdTitleKey + n.Title + "\n")
	if len(n.Tags) > 0 {
		b.WriteString(mdTagsKey + strings.Join(n.Tags, mdTagsJoined) + "\n")
	}
	b.WriteString(mdFence)
	b.WriteString(n.Body)
	return b.Bytes()
}

// DecodeMdNote 解析一篇 Markdown 文件字节。path 是条目路径(相对路径去 .md),
// 仅用于错误定位。front-matter 严格匹配导出格式,违规响亮报错;
// 正文是闭合 --- 行之后的原文字节。
func DecodeMdNote(path string, data []byte) (MdNote, error) {
	n := MdNote{Path: path}
	rest, ok := bytes.CutPrefix(data, []byte(mdFence))
	if !ok {
		return n, fmt.Errorf("md: %q 首行必须是 ---", path)
	}
	var haveTitle, haveTags bool
	for {
		idx := bytes.IndexByte(rest, '\n')
		if idx < 0 {
			return n, fmt.Errorf("md: %q front-matter 未闭合(缺少第二个 ---)", path)
		}
		line := rest[:idx]
		rest = rest[idx+1:]
		if string(line) == "---" {
			break // 闭合,rest 即正文原文字节
		}
		switch {
		case bytes.HasPrefix(line, []byte(mdTitleKey)):
			if haveTitle {
				return n, fmt.Errorf("md: %q front-matter 重复 title 行", path)
			}
			n.Title = string(line[len(mdTitleKey):])
			haveTitle = true
		case bytes.HasPrefix(line, []byte(mdTagsKey)):
			if haveTags {
				return n, fmt.Errorf("md: %q front-matter 重复 tags 行", path)
			}
			n.Tags = ParseMdTags(string(line[len(mdTagsKey):]))
			haveTags = true
		default:
			return n, fmt.Errorf("md: %q front-matter 含无法识别的行 %q", path, line)
		}
	}
	if !haveTitle || strings.TrimSpace(n.Title) == "" {
		return n, fmt.Errorf("md: %q 缺少 title(必填)", path)
	}
	n.Body = string(rest)
	return n, nil
}

// ParseMdTags 解析 tags 值:逗号分隔,段内空白裁剪,空段丢弃。
func ParseMdTags(v string) []string {
	var tags []string
	for _, t := range strings.Split(v, ",") {
		if t = strings.TrimSpace(t); t != "" {
			tags = append(tags, t)
		}
	}
	return tags
}

// ExportMarkdown 列出指定快照(分支名/地址/短标识;缺省当前分支头)的
// 全部条目并转为 Markdown 表示,输出按条目路径排序。
func (r *Repo) ExportMarkdown(ctx context.Context, ref string) ([]MdNote, error) {
	refs, err := r.ListNotesAt(ctx, "", ref)
	if err != nil {
		return nil, err
	}
	docs := make([]MdNote, 0, len(refs))
	for _, nr := range refs {
		docs = append(docs, MdNote{
			Path:  nr.Path,
			Title: nr.Note.Meta.Title,
			Tags:  nr.Note.Meta.Tags,
			Body:  string(nr.Body),
		})
	}
	return docs, nil
}

// ImportMarkdown 把一批 Markdown 表示的笔记写回当前分支:
//   - 与当前条目逐字段(title/tags/正文)一致的跳过——地址不变,不产生任何对象;
//   - 其余走 BulkImport 等价路径:N 条合并为一次提交 + 一次索引增量;
//   - 覆盖时若当前头祖先链上存在内容完全一致的旧条目,复用其 CreatedAt——
//     内容寻址下同字节必得同地址,使 import(export(库)) 在改动/删除后
//     也能逐字节还原(diff 零变更,地址不变)。
//
// 全部条目与当前一致时不产生新快照(Snapshot 为空)。
func (r *Repo) ImportMarkdown(ctx context.Context, docs []MdNote, msg string) (MdImportResult, error) {
	if len(docs) == 0 {
		return MdImportResult{}, errors.New("repo: Markdown 导入为空")
	}
	curRefs, err := r.ListNotes(ctx, "")
	if err != nil {
		return MdImportResult{}, err
	}
	current := make(map[string]*NoteRef, len(curRefs))
	for _, nr := range curRefs {
		current[nr.Path] = nr
	}
	var res MdImportResult
	items := make([]BulkInput, 0, len(docs))
	for _, d := range docs {
		if existing, ok := current[d.Path]; ok && mdNoteEqual(existing, d) {
			res.Unchanged++
			continue
		}
		in := NoteInput{Title: d.Title, Body: d.Body, Tags: d.Tags}
		if t, found := r.mdInheritTime(ctx, d); found {
			in.Time = t
		}
		items = append(items, BulkInput{Path: d.Path, In: in})
	}
	if len(items) == 0 {
		return res, nil
	}
	snap, n, err := r.BulkImport(ctx, items, msg)
	if err != nil {
		return MdImportResult{}, err
	}
	res.Snapshot = snap
	res.Imported = n
	return res, nil
}

// mdNoteEqual 报告库中条目与 Markdown 表示是否逐字段一致(title/tags/正文)。
func mdNoteEqual(nr *NoteRef, d MdNote) bool {
	if nr.Note.Meta.Title != d.Title || string(nr.Body) != d.Body {
		return false
	}
	return tagsEqual(nr.Note.Meta.Tags, d.Tags)
}

// tagsEqual 报告两组标签是否一致(nil 与空组视为一致)。
func tagsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// mdInheritTime 沿当前头祖先链(首父链)寻找与 d 内容完全一致
// (title/tags/正文,正文按内容哈希比对,不落任何对象)的旧条目,
// 返回其 CreatedAt;找不到返回 found=false(新条目用当前时间)。
func (r *Repo) mdInheritTime(ctx context.Context, d MdNote) (int64, bool) {
	head, has, err := r.head(ctx)
	if err != nil || !has {
		return 0, false
	}
	dirs, slug, err := SplitNotePath(d.Path)
	if err != nil {
		return 0, false
	}
	bodyAddr := hash.Sum([]byte(d.Body))
	seen := map[string]bool{}
	cur := head
	for cur != "" && !seen[string(cur)] {
		seen[string(cur)] = true
		snap, err := r.loadSnapshot(ctx, cur)
		if err != nil {
			return 0, false
		}
		if t, err := r.treeAtSnapshot(ctx, cur); err == nil {
			if e, err := r.leafEntry(ctx, t, dirs, slug); err == nil && e.Type == object.EntryNote {
				if n, ok := r.mdMatchNote(ctx, e.Addr, d, bodyAddr); ok {
					return n.Meta.CreatedAt, true
				}
			}
		}
		if len(snap.Parents) == 0 {
			break
		}
		cur = snap.Parents[0]
	}
	return 0, false
}

// mdMatchNote 读取并解码一个 note 对象,与 Markdown 表示比对内容。
func (r *Repo) mdMatchNote(ctx context.Context, addr hash.Address, d MdNote, bodyAddr hash.Address) (*object.Note, bool) {
	data, kind, err := r.st.Get(ctx, addr)
	if err != nil || kind != object.KindNote {
		return nil, false
	}
	n, err := object.DecodeNote(data)
	if err != nil {
		return nil, false
	}
	if n.Meta.Title != d.Title || n.Body != bodyAddr || !tagsEqual(n.Meta.Tags, d.Tags) {
		return nil, false
	}
	return n, true
}
