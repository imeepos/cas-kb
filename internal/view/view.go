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
// Snippet 为可选字段(M4.2 片段高亮):omitempty——仅调用方显式要求
// (--snippet / snippet=1)时存在,缺省输出与旧契约逐字节一致,旧消费者零破坏。
type SearchRow struct {
	Path    string   `json:"path"`
	Slug    string   `json:"slug"`
	Addr    string   `json:"addr"`
	Title   string   `json:"title"`
	Tags    []string `json:"tags"`
	Summary string   `json:"summary"`
	Score   float64  `json:"score"`
	Snippet string   `json:"snippet,omitempty"`
}

// SearchRows 由检索命中构造结果行(顺序保持入参序);不带 snippet 字段。
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

// SearchRowsWithSnippet 同 SearchRows 并逐条附带命中片段(片段是排序后
// 附加的展示信息,绝不影响打分与顺序);query 为原始查询串,词元经与索引
// 同一套分词得到(DESIGN §7.1)。
func SearchRowsWithSnippet(hits []repo.SearchHit, query string) []SearchRow {
	rows := SearchRows(hits)
	for i := range rows {
		rows[i].Snippet = Snippet(hits[i].Body, query)
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

// MergeStateRow 是 GET /api/v1/merge-state 与 kb stage --json 的行契约
// (调研 best-practices-adoption §1.3:单枚举 state + 派生布尔 + 事实字段)。
// state ∈ idle|merging;idle 是轮询稳态而非错误:事实字段为 null、conflicts
// 为空数组、两布尔 false。conflicts 逐条复用 repo.MergeConflict 的 JSON 契约
// (path/kind/base/ours/theirs,全地址)——一份实现两个出口,由 cmd/kb 的
// TestServeMergeStateParity 钉死逐字段相等。
type MergeStateRow struct {
	Project       string               `json:"project"`
	Branch        string               `json:"branch"`
	State         string               `json:"state"` // idle | merging
	CanContinue   bool                 `json:"can_continue"`
	CanAbort      bool                 `json:"can_abort"`
	Base          *string              `json:"base"`
	Theirs        *string              `json:"theirs"`
	Ours          *string              `json:"ours"`
	Conflicts     []repo.MergeConflict `json:"conflicts"`
	ConflictCount int                  `json:"conflict_count"`
	MergedBranch  *string              `json:"merged_branch"` // 中间态分支名 <branch>-merge
}

// conflictsOrEmpty 把 nil 冲突清单归一为空数组,保证 JSON 输出是 [] 而非 null。
func conflictsOrEmpty(cs []repo.MergeConflict) []repo.MergeConflict {
	if cs == nil {
		return []repo.MergeConflict{}
	}
	return cs
}

// addrPtr 把地址转为指针(事实字段 idle 时为 null,合并态始终有值;空基线
// 冷启动的 base 为空串亦如实输出,与 meta 键存储一致)。
func addrPtr(a hash.Address) *string {
	s := string(a)
	return &s
}

// Doctor 状态枚举:退出码契约消费 status(任一 fail ⇒ kb doctor 退出码 1,
// 仅 ok/warn ⇒ 0);细分只存在于行内,退出码不做多档。
const (
	DoctorStatusOK   = "ok"
	DoctorStatusWarn = "warn"
	DoctorStatusFail = "fail"
)

// DoctorRow 是 kb doctor --json 的行契约:检查名(注册表名即契约,新增不破坏
// 旧名)+ 三档状态 + 一句人话详情(含可行动修复建议)。detail 由 doctor 保证
// 绝不回显连接串凭据段与令牌值。
type DoctorRow struct {
	Check  string `json:"check"`
	Status string `json:"status"` // ok | warn | fail
	Detail string `json:"detail"`
}

// MergeStateRowOf 构造合并状态行;st 为 nil 表示无合并中态(idle 稳态)。
// project/branch 为生效作用域(回显);merged_branch 按中间态分支命名规则
// (<branch>-merge)派生。合并态事实字段取 repo.MergeState(meta 键)原值,
// 派生布尔由 state 唯一决定(「能否收束」由冲突清单与冻结纪律完备决定)。
func MergeStateRowOf(project, branch string, st *repo.MergeState) MergeStateRow {
	if st == nil {
		return MergeStateRow{
			Project:   project,
			Branch:    branch,
			State:     "idle",
			Conflicts: []repo.MergeConflict{},
		}
	}
	merged := branch + "-merge"
	return MergeStateRow{
		Project:       project,
		Branch:        branch,
		State:         "merging",
		CanContinue:   true,
		CanAbort:      true,
		Base:          addrPtr(st.Base),
		Theirs:        addrPtr(st.Theirs),
		Ours:          addrPtr(st.Ours),
		Conflicts:     conflictsOrEmpty(st.Conflicts),
		ConflictCount: len(st.Conflicts),
		MergedBranch:  &merged,
	}
}
