// Package view 定义 CLI --json 与只读 HTTP API(/api/v1)共用的 JSON 行契约。
// 同一组结构体保证两条出口的字段名、字段序与派生规则(摘要、短标识)逐字段一致;
// 变更字段必须同步 DESIGN §8.5 与 CLI 文档,并由 cmd/kb 的 TestServeCLIParity 钉死。
package view

import (
	"strings"
	"time"

	"github.com/imeepos/cas-kb/internal/hash"
	"github.com/imeepos/cas-kb/internal/repo"
	"github.com/imeepos/cas-kb/internal/store"
)

// summaryRunes 是派生摘要的最大字符数(展示层派生,不改对象)。
const summaryRunes = 120

// Summary 从正文派生首个非空行摘要,超长截断,供 AI 粗筛;不改对象与地址。
func Summary(body []byte) string {
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		rs := []rune(line)
		if len(rs) > summaryRunes {
			return string(rs[:summaryRunes]) + "…"
		}
		return line
	}
	return ""
}

// shortAddrLen 是地址短标识的固定截断长度(kb log 首列同款)。
const shortAddrLen = 16

// ShortAddr 截断地址为短标识;kb log 首列与 /api/v1/log 的 id、parents 同构。
func ShortAddr(a hash.Address) string {
	if len(a) > shortAddrLen {
		return string(a[:shortAddrLen])
	}
	return string(a)
}

// tagsOrEmpty 把 nil 标签归一为空数组,保证 JSON 输出是 [] 而非 null。
func tagsOrEmpty(tags []string) []string {
	if tags == nil {
		return []string{}
	}
	return tags
}

// ProjectRow 是 project ls --json 与 GET /api/v1/projects 的行契约(DESIGN §4.6)。
type ProjectRow struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Branches    int    `json:"branches"`
}

// ProjectRows 由 store 项目统计构造清单行(顺序保持入参序:字典序)。
func ProjectRows(stats []store.ProjectStat) []ProjectRow {
	rows := make([]ProjectRow, 0, len(stats))
	for _, st := range stats {
		rows = append(rows, ProjectRow{Name: st.Project, Description: st.Description, Branches: st.Branches})
	}
	return rows
}

// NoteLsRow 是 note ls --json 的行契约(schema v3 派生摘要)。
type NoteLsRow struct {
	Path      string   `json:"path"`
	Slug      string   `json:"slug"`
	Title     string   `json:"title"`
	Tags      []string `json:"tags"`
	CreatedAt int64    `json:"created_at"`
	Summary   string   `json:"summary"`
}

// NoteLsRows 由条目视图构造清单行(顺序保持入参序:路径字典序)。
func NoteLsRows(refs []*repo.NoteRef) []NoteLsRow {
	rows := make([]NoteLsRow, 0, len(refs))
	for _, ref := range refs {
		rows = append(rows, NoteLsRow{
			Path: ref.Path, Slug: ref.Slug, Title: ref.Note.Meta.Title,
			Tags: tagsOrEmpty(ref.Note.Meta.Tags), CreatedAt: ref.Note.Meta.CreatedAt,
			Summary: Summary(ref.Body),
		})
	}
	return rows
}

// SearchRow 是 search --json 与 GET /api/v1/search 的行契约。
// 行序即检索的确定性排序(分数降序 → 路径升序 → 地址),调用方不得重排。
type SearchRow struct {
	Path    string   `json:"path"`
	Slug    string   `json:"slug"`
	Addr    string   `json:"addr"`
	Title   string   `json:"title"`
	Tags    []string `json:"tags"`
	Summary string   `json:"summary"`
	Score   float64  `json:"score"`
}

// SearchRows 由检索命中构造结果行(顺序保持入参序)。
func SearchRows(hits []repo.SearchHit) []SearchRow {
	rows := make([]SearchRow, 0, len(hits))
	for _, h := range hits {
		rows = append(rows, SearchRow{
			Path: h.Path, Slug: h.Slug, Addr: string(h.Addr), Title: h.Title,
			Tags: tagsOrEmpty(h.Tags), Summary: Summary(h.Body), Score: h.Score,
		})
	}
	return rows
}

// NoteRow 是 GET /api/v1/note 的单条笔记契约(正文原文 + 派生摘要)。
type NoteRow struct {
	Path    string   `json:"path"`
	Title   string   `json:"title"`
	Tags    []string `json:"tags"`
	Body    string   `json:"body"`
	Summary string   `json:"summary"`
}

// NoteRowOf 由单条条目视图构造。
func NoteRowOf(ref *repo.NoteRef) NoteRow {
	return NoteRow{
		Path: ref.Path, Title: ref.Note.Meta.Title,
		Tags: tagsOrEmpty(ref.Note.Meta.Tags),
		Body: string(ref.Body), Summary: Summary(ref.Body),
	}
}

// LogRow 是 GET /api/v1/log 的行契约,字段与 kb log 展示列对齐:
// id=短标识,时间与 CLI 同格式,parents 为短标识数组(首元素即 CLI 的 parent= 列)。
type LogRow struct {
	ID      string   `json:"id"`
	Time    string   `json:"time"`
	Message string   `json:"message"`
	Parents []string `json:"parents"`
}

// LogTimeFormat 与 kb log 文本输出的时间列同格式。
const LogTimeFormat = "2006-01-02 15:04:05"

// LogRows 由快照链构造行(最新在前,顺序保持入参序);limit<=0 表示不截断。
func LogRows(entries []repo.LogEntry, limit int) []LogRow {
	if limit > 0 && limit < len(entries) {
		entries = entries[:limit]
	}
	rows := make([]LogRow, 0, len(entries))
	for _, e := range entries {
		parents := make([]string, 0, len(e.Snapshot.Parents))
		for _, p := range e.Snapshot.Parents {
			parents = append(parents, ShortAddr(p))
		}
		rows = append(rows, LogRow{
			ID:      ShortAddr(e.Addr),
			Time:    time.Unix(e.Snapshot.Time, 0).Format(LogTimeFormat),
			Message: e.Snapshot.Message,
			Parents: parents,
		})
	}
	return rows
}

// DiffRow 是 diff --json 与 GET /api/v1/diff 的行契约:
// type ∈ added|removed|updated,条目按全路径,输出按路径字典序(repo.Diff 保证)。
type DiffRow struct {
	Path string `json:"path"`
	Type string `json:"type"`
	From string `json:"from"`
	To   string `json:"to"`
}

// DiffRows 由 repo.Diff 的变更集构造行(顺序保持入参序)。
func DiffRows(changes []repo.Change) []DiffRow {
	rows := make([]DiffRow, 0, len(changes))
	for _, c := range changes {
		rows = append(rows, DiffRow{Path: c.Path, Type: string(c.Type), From: string(c.From), To: string(c.To)})
	}
	return rows
}
