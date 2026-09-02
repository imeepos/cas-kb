// Package server 实现 kb serve 的 HTTP API(DESIGN §8.5 只读 + §8.6 写入型):
// 让 AI/Agent 免 shell 消费并写入知识库。读端点保持只读 GET;写入端点
// (POST /api/v1/note、DELETE /api/v1/note)直接复用 repo.SetNote/RemoveNote,
// 绝不另写第二套写逻辑。未配置令牌时服务保持纯只读:写端点一律 403,
// 一切行为与 v0.4.0 只读 serve 完全一致。JSON 行契约复用 internal/view,
// 与 CLI --json 输出同构(cmd/kb TestServeCLIParity 钉死)。
package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/imeepos/cas-kb/internal/object"
	"github.com/imeepos/cas-kb/internal/repo"
	"github.com/imeepos/cas-kb/internal/store"
	"github.com/imeepos/cas-kb/internal/view"
)

// Options 是 Server 的构造选项,字段语义与 CLI 全局环境一致。
type Options struct {
	// DSN 是库连接串(KB_DSN 同口径:SQLite 路径或 postgres://…),两后端都可 serve。
	DSN string
	// Project 是项目作用域(空串按 default)。
	Project string
	// Branch 是默认分支名(空串按 main)。
	Branch string
	// Token 是写入令牌(KB_SERVE_TOKEN/--token 解析后的值);空串=未配置,
	// 服务保持纯只读,写端点一律 403。令牌只从内存比较,绝不写日志/回显。
	Token string
}

// Server 持有一个已打开的仓库视图;读路径消费 repo/store 的读方法,
// 写路径直接复用 repo.SetNote/RemoveNote(语义与 CLI 逐字段一致)。
type Server struct {
	st      store.Store
	r       *repo.Repo
	backend string // sqlite | postgres
	target  string // 展示目标(绝不含凭据)
	project string
	token   string // 空串=未配置写入令牌(纯只读模式)
}

// New 打开存储并构造 Server;调用方负责 Close。
func New(ctx context.Context, opts Options) (*Server, error) {
	s, err := store.Open(ctx, opts.DSN)
	if err != nil {
		return nil, err
	}
	name, target := store.DescribeBackend(opts.DSN)
	r := repo.Open(s, repo.Config{Project: opts.Project, Branch: opts.Branch})
	return &Server{st: s, r: r, backend: name, target: target, project: r.Project(), token: opts.Token}, nil
}

// Backend 返回后端名(sqlite|postgres)与展示目标(不含凭据),供启动横幅打印。
func (s *Server) Backend() (name, target string) { return s.backend, s.target }

// Project 返回生效的项目作用域。
func (s *Server) Project() string { return s.project }

// Branch 返回默认分支名。
func (s *Server) Branch() string { return s.r.Branch() }

// Close 释放底层存储连接。
func (s *Server) Close() error { return s.st.Close() }

// Handler 返回路由。读端点 GET-only(非 GET 一律 405);
// 写入端点 POST/DELETE /api/v1/note 需要 Bearer 令牌鉴权(未配置令牌时一律 403);
// 未知路径 404;错误响应一律 {"error":"…"}。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.onlyGET(s.handleHealth))
	mux.HandleFunc("/api/v1/projects", s.onlyGET(s.handleProjects))
	mux.HandleFunc("/api/v1/tree", s.onlyGET(s.handleTree))
	mux.HandleFunc("/api/v1/note", s.handleNoteRoute) // GET 读 + POST 写 + DELETE 写
	mux.HandleFunc("/api/v1/search", s.onlyGET(s.handleSearch))
	mux.HandleFunc("/api/v1/log", s.onlyGET(s.handleLog))
	mux.HandleFunc("/api/v1/diff", s.onlyGET(s.handleDiff))
	mux.HandleFunc("/api/v1/merge-state", s.onlyGET(s.handleMergeState))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		s.writeError(w, http.StatusNotFound, fmt.Errorf("未知端点: %s %s", r.Method, r.URL.Path))
	})
	return mux
}

// onlyGET 把只读端点收紧为 GET-only:写方法一律 405(只读纪律,连 POST 都不例外)。
// 注意:/api/v1/note 不走本包装(它承载 GET 读 + POST/DELETE 写,见 handleNoteRoute)。
func (s *Server) onlyGET(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			s.writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("只读 API:仅支持 GET,收到 %s", r.Method))
			return
		}
		h(w, r)
	}
}

// handleNoteRoute 按方法分发 /api/v1/note:
//   - GET    → 读单条(handleNote)
//   - POST   → 写入(POST /api/v1/note,handleNoteCreate)
//   - DELETE → 删除(DELETE /api/v1/note?path=,handleNoteDelete)
//   - 其余   → 405
func (s *Server) handleNoteRoute(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleNote(w, r)
	case http.MethodPost:
		s.handleNoteCreate(w, r)
	case http.MethodDelete:
		s.handleNoteDelete(w, r)
	default:
		w.Header().Set("Allow", "GET, POST, DELETE")
		s.writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("/api/v1/note 仅支持 GET/POST/DELETE,收到 %s", r.Method))
	}
}

// writeDisabledErr 是未配置写入令牌时写端点的一律 403 文案(DESIGN §8.6)。
var writeDisabledErr = errors.New("服务未配置写入令牌,当前为只读模式;设置 KB_SERVE_TOKEN 后启用")

// writeMode 报告服务是否启用了写入令牌。
func (s *Server) writeMode() bool { return s.token != "" }

// requireWriteToken 校验写请求的 Bearer 令牌。未配置令牌 → 403(纯只读降级);
// 已配置但缺头/错令牌 → 401。令牌只从内存常量时间比较,永不写日志/回显。
func (s *Server) requireWriteToken(w http.ResponseWriter, r *http.Request) bool {
	if !s.writeMode() {
		s.writeError(w, http.StatusForbidden, writeDisabledErr)
		return false
	}
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, prefix) {
		s.writeError(w, http.StatusUnauthorized, errors.New("缺少写入令牌:请求需带 Authorization: Bearer <token> 头"))
		return false
	}
	got := strings.TrimSpace(h[len(prefix):])
	if len(got) != len(s.token) || subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) != 1 {
		s.writeError(w, http.StatusUnauthorized, errors.New("写入令牌无效(Authorization: Bearer <token>)"))
		return false
	}
	return true
}

// handleHealth 服务 GET /healthz → {"ok":true,"backend":…,"schema_version":N,"project":…}
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	v, err := s.st.MetaGet(r.Context(), "schema_version")
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err)
		return
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, fmt.Errorf("schema_version %q 非数字: %w", v, err))
		return
	}
	s.writeJSON(w, http.StatusOK, healthRow{OK: true, Backend: s.backend, SchemaVersion: n, Project: s.project})
}

// healthRow 是 /healthz 的契约。
type healthRow struct {
	OK            bool   `json:"ok"`
	Backend       string `json:"backend"`
	SchemaVersion int    `json:"schema_version"`
	Project       string `json:"project"`
}

// handleProjects 服务 GET /api/v1/projects → project ls --json 同构。
func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request) {
	stats, err := s.st.ProjectStats(r.Context())
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err)
		return
	}
	s.writeJSON(w, http.StatusOK, view.ProjectRows(stats))
}

// treeNode 是 /api/v1/tree 的嵌套节点契约:dir 节点带 children,
// note 节点带 addr/title。
type treeNode struct {
	Path     string      `json:"path"`
	Name     string      `json:"name"`
	Type     string      `json:"type"` // dir | note
	Addr     string      `json:"addr,omitempty"`
	Title    string      `json:"title,omitempty"`
	Children []*treeNode `json:"children,omitempty"`
}

// treeNodeOf 把 repo.DirNode 递归转成 JSON 节点(顺序保持 repo 的字典序)。
func treeNodeOf(n *repo.DirNode) *treeNode {
	out := &treeNode{Path: n.Path, Name: n.Name, Type: string(n.Type)}
	if n.Type == object.EntryNote {
		out.Addr = string(n.Addr)
		out.Title = n.Title
		return out
	}
	for _, c := range n.Children {
		out.Children = append(out.Children, treeNodeOf(c))
	}
	return out
}

// handleTree 服务 GET /api/v1/tree?at=<短标识|分支名> → 当前项目树(JSON 嵌套;
// at 省略=分支头)。
func (s *Server) handleTree(w http.ResponseWriter, r *http.Request) {
	root, err := s.r.DirTreeAt(r.Context(), "", r.URL.Query().Get("at"))
	if err != nil {
		s.fail(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, treeNodeOf(root))
}

// handleNote 服务 GET /api/v1/note?path=<全路径>&at= → 单条笔记;不存在 404。
func (s *Server) handleNote(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	path := q.Get("path")
	if strings.TrimSpace(path) == "" {
		s.writeError(w, http.StatusBadRequest, errors.New("缺少必填参数 path(条目全路径)"))
		return
	}
	// 先行格式校验:空段/保留段等路径问题 → 400,与「目标不存在 → 404」区分清晰
	if _, err := repo.ParsePath(path); err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}
	at := q.Get("at")
	var ref *repo.NoteRef
	var err error
	if at == "" {
		ref, err = s.r.Note(r.Context(), path)
	} else {
		ref, err = s.r.NoteAt(r.Context(), path, at)
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, view.NoteRowOf(ref))
}

// handleSearch 服务 GET /api/v1/search?q=<查询>&at=&limit=&snippet= →
// search --json 同构;行序即检索的确定性排序,limit 只截断不重排。
// snippet=1(M4.2 片段高亮)为可选展示参数:行内附带 snippet 字段(语义与
// CLI --snippet 相同);缺省不带,契约与旧消费者零破坏(DESIGN §7.1)。
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	query := q.Get("q")
	if strings.TrimSpace(query) == "" {
		s.writeError(w, http.StatusBadRequest, errors.New("缺少必填参数 q(查询词)"))
		return
	}
	limit, err := parseLimit(q.Get("limit"))
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}
	hits, err := s.r.Search(r.Context(), query, q.Get("at"))
	if err != nil {
		s.fail(w, err)
		return
	}
	rows := view.SearchRows(hits)
	if q.Get("snippet") == "1" { // 仅字面 1 生效;片段是排序后附加的展示信息
		rows = view.SearchRowsWithSnippet(hits, query)
	}
	if limit > 0 && limit < len(rows) {
		rows = rows[:limit]
	}
	s.writeJSON(w, http.StatusOK, rows)
}

// handleLog 服务 GET /api/v1/log?limit= → 快照链(最新在前),
// 字段与 kb log 展示列对齐(短标识/时间/消息/父快照)。
func (s *Server) handleLog(w http.ResponseWriter, r *http.Request) {
	limit, err := parseLimit(r.URL.Query().Get("limit"))
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}
	entries, err := s.r.Log(r.Context())
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err)
		return
	}
	s.writeJSON(w, http.StatusOK, view.LogRows(entries, limit))
}

// handleDiff 服务 GET /api/v1/diff?from=<短标识|分支名>&to= → A/D/M 按全路径,
// 与 kb diff --json 同构(共用 view.DiffRows,差异逻辑只有 repo.Diff 一份)。
func (s *Server) handleDiff(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	from, to := q.Get("from"), q.Get("to")
	if from == "" || to == "" {
		s.writeError(w, http.StatusBadRequest, errors.New("缺少必填参数 from 与 to(分支名、地址或短标识)"))
		return
	}
	changes, err := s.r.Diff(r.Context(), from, to)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, view.DiffRows(changes))
}

// handleMergeState 服务 GET /api/v1/merge-state?project=&branch= → 合并中间态
// 查询(调研 best-practices-adoption §1.3):单枚举 state(idle|merging)+ 派生
// 布尔 + 事实字段。无合并中态返回 200 + state:"idle"(自动化轮询的稳态,不是
// 错误);项目或分支不存在才 404;参数显式给空白值 400。合并态事实复用
// repo.MergeState 的读取(meta 键一份实现,不另写解析),行契约 view.MergeStateRowOf
// 与 kb stage --json 同构(cmd/kb TestServeMergeStateParity 钉死)。
func (s *Server) handleMergeState(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	project, branch := s.project, s.r.Branch()
	for name, dst := range map[string]*string{"project": &project, "branch": &branch} {
		vals, ok := q[name]
		if !ok {
			continue // 省略参数即取 serve 进程作用域,与现有端点参数习惯一致
		}
		if strings.TrimSpace(vals[0]) == "" {
			s.writeError(w, http.StatusBadRequest, fmt.Errorf("参数 %s 不能为空白(省略即取 serve 作用域)", name))
			return
		}
		*dst = vals[0]
	}
	if _, err := s.st.ProjectGet(r.Context(), project); err != nil {
		if errors.Is(err, store.ErrProjectNotFound) {
			s.writeError(w, http.StatusNotFound, fmt.Errorf("项目 %q 不存在: %w", project, err))
			return
		}
		s.writeError(w, http.StatusInternalServerError, err)
		return
	}
	if _, err := s.st.BranchGet(r.Context(), project, branch); err != nil {
		if errors.Is(err, store.ErrBranchNotFound) {
			s.writeError(w, http.StatusNotFound, fmt.Errorf("分支 %q 不存在(项目 %s): %w", branch, project, err))
			return
		}
		s.writeError(w, http.StatusInternalServerError, err)
		return
	}
	rv := repo.Open(s.st, repo.Config{Project: project, Branch: branch, NoIndex: true})
	ms, err := rv.MergeState(r.Context())
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err)
		return
	}
	s.writeJSON(w, http.StatusOK, view.MergeStateRowOf(project, branch, ms))
}

// parseLimit 解析 limit 参数:空串=不限制;必须正整数。
func parseLimit(raw string) (int, error) {
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("limit 需要正整数,得到 %q", raw)
	}
	return n, nil
}

// fail 把 repo/store 错误映射为状态码:
// 参数层面的(短标识歧义、路径段类型冲突)→ 400;目标不存在 → 404;其余 → 500。
func (s *Server) fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, repo.ErrAmbiguousRef), errors.Is(err, repo.ErrEntryTypeConflict):
		s.writeError(w, http.StatusBadRequest, err)
	case errors.Is(err, store.ErrNotFound), errors.Is(err, store.ErrBranchNotFound), errors.Is(err, store.ErrProjectNotFound):
		s.writeError(w, http.StatusNotFound, err)
	default:
		s.writeError(w, http.StatusInternalServerError, err)
	}
}

// writeJSON 输出 JSON:与 CLI printJSON 同款设置(2 空格缩进、不转义 HTML)。
func (s *Server) writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

// writeError 输出 {"error":"…"} 与状态码。
func (s *Server) writeError(w http.ResponseWriter, code int, err error) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	_ = enc.Encode(map[string]string{"error": err.Error()})
}
