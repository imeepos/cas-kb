// 写入型 HTTP API(DESIGN §8.6):POST /api/v1/note 与 DELETE /api/v1/note。
// 语义与 CLI note set / note rm 逐字段一致,直接复用 repo.SetNote/RemoveNote,
// 绝不另写第二套写逻辑;每次写后 fsck 可过、检索立即可见(索引增量同步完成)。
package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/imeepos/cas-kb/internal/repo"
	"github.com/imeepos/cas-kb/internal/store"
	"github.com/imeepos/cas-kb/internal/view"
)

// noteCreateReq 是 POST /api/v1/note 的请求体契约(与 kb note set 入参一一对应):
// path/title/body 必填,tags 可选。语义与 CLI 逐字段一致,直接复用 repo.SetNote。
type noteCreateReq struct {
	Path  string   `json:"path"`
	Title string   `json:"title"`
	Tags  []string `json:"tags"`
	Body  string   `json:"body"`
}

// noteWriteResp 是 POST /api/v1/note 成功(201)的响应契约。
type noteWriteResp struct {
	Path    string `json:"path"`
	Address string `json:"address"` // note 对象地址
	Short   string `json:"short"`   // 新快照短标识
}

// noteRemoveResp 是 DELETE /api/v1/note 成功(200)的响应契约。
type noteRemoveResp struct {
	Removed int    `json:"removed"` // 删除计数(等价 CLI note rm,恒为 1)
	Short   string `json:"short"`   // 新快照短标识
}

// handleNoteCreate 服务 POST /api/v1/note:等价 kb note set。
// body JSON {"path","title","tags"?,"body"};成功 201 + {"path","address","short"};
// 参数缺失/非法路径/路径是目录等 → 400 沿用 CLI 的可行动报错文案;
// 未配置写入令牌一律 403;令牌缺失/错误 401;锁忙 503。
func (s *Server) handleNoteCreate(w http.ResponseWriter, r *http.Request) {
	if !s.requireWriteToken(w, r) {
		return
	}
	var in noteCreateReq
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(&in); err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Errorf("请求体需为 JSON {path,title,tags?,body}: %w", err))
		return
	}
	// 参数校验,错误文案与 CLI 一致(可行动,不堆栈)
	if strings.TrimSpace(in.Path) == "" {
		s.writeError(w, http.StatusBadRequest, errors.New("note set: 缺少 path(条目全路径)"))
		return
	}
	if strings.TrimSpace(in.Title) == "" {
		s.writeError(w, http.StatusBadRequest, errors.New("note set: 缺少 title(条目标题)"))
		return
	}
	// 先行格式校验:空段/保留段等路径问题 → 400(与 CLI SplitNotePath 同一入口)
	if _, _, err := repo.SplitNotePath(in.Path); err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}
	snapAddr, noteAddr, err := s.r.SetNote(r.Context(), in.Path, repo.NoteInput{Title: in.Title, Body: in.Body, Tags: in.Tags}, "note set")
	if err != nil {
		s.failWrite(w, err)
		return
	}
	s.writeJSON(w, http.StatusCreated, noteWriteResp{
		Path:    in.Path,
		Address: string(noteAddr),
		Short:   view.ShortAddr(snapAddr),
	})
}

// handleNoteDelete 服务 DELETE /api/v1/note?path=<全路径>:等价 kb note rm。
// 成功 200 + {"removed":1,"short"};路径不存在 404;路径是目录 400;
// 未配置写入令牌一律 403;令牌缺失/错误 401;锁忙 503。
func (s *Server) handleNoteDelete(w http.ResponseWriter, r *http.Request) {
	if !s.requireWriteToken(w, r) {
		return
	}
	path := r.URL.Query().Get("path")
	if strings.TrimSpace(path) == "" {
		s.writeError(w, http.StatusBadRequest, errors.New("缺少必填参数 path(条目全路径)"))
		return
	}
	if _, _, err := repo.SplitNotePath(path); err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}
	snapAddr, err := s.r.RemoveNote(r.Context(), path, "note rm")
	if err != nil {
		s.failWrite(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, noteRemoveResp{Removed: 1, Short: view.ShortAddr(snapAddr)})
}

// failWrite 把写端点(repo.SetNote/RemoveNote)的错误映射为状态码:
// 参数层面(非法路径、路径是目录/条目类型冲突)→ 400;目标不存在 → 404;
// 写入事务锁忙(serve 与 CLI 并发写)→ 503 + 可行动提示;其余 → 500。
func (s *Server) failWrite(w http.ResponseWriter, err error) {
	switch {
	case store.IsLockBusy(err):
		s.writeError(w, http.StatusServiceUnavailable, fmt.Errorf("知识库正被其他写入占用(serve 与 CLI 同时写);请稍后重试或改用 CLI: %w", err))
	case errors.Is(err, store.ErrNotFound):
		s.writeError(w, http.StatusNotFound, err)
	case errors.Is(err, store.ErrBranchNotFound), errors.Is(err, store.ErrProjectNotFound):
		s.writeError(w, http.StatusNotFound, err)
	case errors.Is(err, repo.ErrEntryTypeConflict), isWritePathConflict(err):
		s.writeError(w, http.StatusBadRequest, err)
	default:
		s.writeError(w, http.StatusInternalServerError, err)
	}
}

// isWritePathConflict 识别 repo.SetNote/RemoveNote 的「客户端路径问题」错误
// (目标是目录、中间段是条目),这些是调用方路径引发的,映射为 400。
// repo 层这些错误是纯 fmt.Errorf(文案与 CLI 一致),按稳定文案识别,
// 与 repo.translateBranchSetErr 的字符串识别口径一致。
func isWritePathConflict(err error) bool {
	if err == nil {
		return false
	}
	for _, marker := range []string{
		"是目录,不能作为条目写入", // SetNote:目标同名是目录
		"是条目,不能作为目录",   // mutateAt:中间段是条目
		"是目录,请用 dir rm 删除", // RemoveNote:目标是目录
	} {
		if strings.Contains(err.Error(), marker) {
			return true
		}
	}
	return false
}