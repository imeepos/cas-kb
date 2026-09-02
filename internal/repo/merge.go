package repo

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/imeepos/cas-kb/internal/hash"
	"github.com/imeepos/cas-kb/internal/object"
	"github.com/imeepos/cas-kb/internal/store"
)

// 三方合并(M5-A 批次,调研 docs/research/merge-design.md §2):
//
//	LCA(ours, theirs) → base          (§2.2,沿 parents 链 BFS,不信任 Time)
//	threeWayMerge(base, ours, theirs)  (§2.3,按全路径逐 slug 比较 (type, addr) 三元组)
//	    → { 合并树(合成中间表示), conflicts[] }
//	conflicts 为空 → 落库:合成树自底向上写新对象(结构共享)→ 索引增量(与 commit
//	                 同路径)→ 合并快照 Parents=[ours, theirs] → 推进分支指针
//	conflicts 非空 → 全有或全无:不动分支指针、不产生正式提交,返回结构化冲突清单
//
// 全程对象幂等,任何一步失败重试安全;最坏留下未达对象交 GC。
// 中间态(<branch>-merge 分支 + meta 键 + 冲突清单输出)属 B 批次,本文件只交付
// repo 内核:LCA、三方树合并纯函数与零冲突落库。

// 冲突类别(§2.5.2 输出契约:kind ∈ {content, modify-delete, type})。
const (
	mergeKindContent      = "content"       // 双侧异改(含 add/add)
	mergeKindModifyDelete = "modify-delete" // 删改对撞(一侧删、另一侧改/换型)
	mergeKindType         = "type"          // note↔dir 形态对撞
)

// mergeDefaultMessage 是合并提交的默认消息(B 批次 CLI 可覆盖)。
const mergeDefaultMessage = "merge theirs"

// MergeConflict 是一条合并冲突。字段名与结构即契约(--json 输出逐字段对应,
// 调整须在调研文档与 ROADMAP 显式记录);地址为空串表示该侧无此路径(⊥)。
type MergeConflict struct {
	Path   string       `json:"path"`
	Kind   string       `json:"kind"`
	Base   hash.Address `json:"base"`
	Ours   hash.Address `json:"ours"`
	Theirs hash.Address `json:"theirs"`
}

// ErrMergeNoCommonHistory 表示两分支头没有共同祖先(如两库各自 init),
// 无法三方合并。文案可行动:确认两端同源,或显式改用覆盖语义。
var ErrMergeNoCommonHistory = errors.New("repo: 两库无共同历史,无法三方合并(确认两端同源,或改用 --force 覆盖)")

// ErrMergeMultipleBases 表示检出多个最近公共祖先(蟹状历史)。
// v1 策略(调研 §2.2 方案甲):响亮拒绝并列出全部候选,由调用方以
// MergeOptions.Base 显式指定其中之一(假干净比报错危险得多)。
type ErrMergeMultipleBases struct {
	Candidates []hash.Address // 全部候选,按地址字典序
}

func (e *ErrMergeMultipleBases) Error() string {
	ids := make([]string, 0, len(e.Candidates))
	for _, a := range e.Candidates {
		ids = append(ids, mergeShortID(a))
	}
	return fmt.Sprintf("repo: 检出 %d 个最近公共祖先(蟹状历史),请用 --base 显式指定其一: %s",
		len(e.Candidates), strings.Join(ids, " "))
}

// ErrMergeConflicts 表示三方合并检出冲突,未落任何对象与指针(全有或全无)。
type ErrMergeConflicts struct {
	Conflicts []MergeConflict
}

func (e *ErrMergeConflicts) Error() string {
	lines := make([]string, 0, len(e.Conflicts))
	for _, c := range e.Conflicts {
		lines = append(lines, fmt.Sprintf("  %s  %s  base %s  ours %s  theirs %s",
			c.Path, c.Kind, mergeShortID(c.Base), mergeShortID(c.Ours), mergeShortID(c.Theirs)))
	}
	return fmt.Sprintf("repo: 合并检出 %d 条冲突,未落库(逐条裁决后重试):\n%s",
		len(e.Conflicts), strings.Join(lines, "\n"))
}

// mergeShortID 返回地址短标识(前缀 + 8 位 hex),仅用于错误文案。
func mergeShortID(a hash.Address) string {
	s := string(a)
	n := len(hash.PrefixSha256) + 8
	if len(s) <= n {
		if s == "" {
			return "(无)"
		}
		return s
	}
	return s[:n]
}

// MergeBaseResult 是基准计算结果。
type MergeBaseResult struct {
	Base       hash.Address   // 选定基准(唯一 LCA 或显式指定)
	Candidates []hash.Address // 多 LCA 时为全部候选(按地址字典序);唯一时与 Base 相同
}

// MergeBase 计算 ours 与 theirs 的最近公共祖先快照:沿 parents 链 BFS 求两侧
// 祖先闭包(含自身)的交集,再剔除可由其他共同祖先可达者,保留「最深」集合
// (调研 §2.2)。祖先判定只认 parents 链,不信任 Time。
// 共祖先集为空 → ErrMergeNoCommonHistory;最深候选多于一个 → ErrMergeMultipleBases,
// 此时 explicit(分支名/完整地址/短标识)可显式指定其中之一。
func (r *Repo) MergeBase(ctx context.Context, ours, theirs hash.Address, explicit string) (MergeBaseResult, error) {
	depth, err := r.ancestorDepths(ctx, ours) // BFS:ours 祖先闭包(含自身)
	if err != nil {
		return MergeBaseResult{}, err
	}
	theirAncestors, err := r.ancestorDepths(ctx, theirs)
	if err != nil {
		return MergeBaseResult{}, err
	}
	common := make([]hash.Address, 0, 8)
	for a := range depth {
		if _, ok := theirAncestors[a]; ok {
			common = append(common, a)
		}
	}
	if len(common) == 0 {
		return MergeBaseResult{}, ErrMergeNoCommonHistory
	}
	// M = { c ∈ C | 不存在 c' ∈ C, c'≠c 使 c 可由 c' 沿 parents 可达 }。
	// 两两可达性校验 O(|C|²) 次 BFS(调研 §2.2:v1 量级——个人库快照数千级——
	// 直接可接受,优化列演进项)。注意 BFS 首达深度与可达性之间无严格序
	// (子可经更长路径浅于父),深度不能替代两两校验;结果按地址排序保证确定。
	maximal := make([]hash.Address, 0, 2)
	for _, c := range common {
		isMaximal := true
		for _, other := range common {
			if other == c {
				continue
			}
			reachable, err := r.snapshotReaches(ctx, other, c)
			if err != nil {
				return MergeBaseResult{}, err
			}
			if reachable {
				isMaximal = false
				break
			}
		}
		if isMaximal {
			maximal = append(maximal, c)
		}
	}
	sort.Slice(maximal, func(i, j int) bool { return maximal[i] < maximal[j] })
	res := MergeBaseResult{Candidates: maximal}
	if explicit == "" {
		if len(maximal) > 1 {
			return res, &ErrMergeMultipleBases{Candidates: maximal}
		}
		res.Base = maximal[0]
		return res, nil
	}
	// 显式基准一旦提供即为权威:必须是最近公共祖先候选之一(唯一 LCA 时
	// 指定其他快照同样拒绝——假干净比报错危险得多)。
	ref, err := r.Resolve(ctx, explicit)
	if err != nil {
		return res, fmt.Errorf("repo: 显式基准 %q 无法解析: %w", explicit, err)
	}
	for _, c := range maximal {
		if c == ref {
			res.Base = ref
			return res, nil
		}
	}
	ids := make([]string, 0, len(maximal))
	for _, c := range maximal {
		ids = append(ids, mergeShortID(c))
	}
	return res, fmt.Errorf("repo: 显式基准 %s 不是最近公共祖先候选(候选: %s)", ref, strings.Join(ids, " "))
}

// ancestorDepths 从 start 沿 parents BFS,返回祖先闭包(含自身)及 BFS 首达深度
// (与 snapshotDepths 同款:首达即最浅)。
func (r *Repo) ancestorDepths(ctx context.Context, start hash.Address) (map[hash.Address]int, error) {
	depth := map[hash.Address]int{start: 0}
	queue := make([]hash.Address, 0, 16)
	queue = append(queue, start)
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		snap, err := r.loadSnapshot(ctx, cur)
		if err != nil {
			return nil, err
		}
		for _, p := range snap.Parents {
			if _, ok := depth[p]; ok {
				continue
			}
			depth[p] = depth[cur] + 1
			queue = append(queue, p)
		}
	}
	return depth, nil
}

// snapshotReaches 报告 to 是否可由 from 沿 parents 链可达(含相等)。
func (r *Repo) snapshotReaches(ctx context.Context, from, to hash.Address) (bool, error) {
	if from == to {
		return true, nil
	}
	seen := map[string]bool{string(from): true}
	queue := make([]hash.Address, 0, 16)
	queue = append(queue, from)
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		snap, err := r.loadSnapshot(ctx, cur)
		if err != nil {
			return false, err
		}
		for _, p := range snap.Parents {
			if p == to {
				return true, nil
			}
			if !seen[string(p)] {
				seen[string(p)] = true
				queue = append(queue, p)
			}
		}
	}
	return false, nil
}

// mergeVal 是一个条目值 (type, addr);typ 为空串表示该侧无此路径(⊥)。
type mergeVal struct {
	typ  object.EntryType
	addr hash.Address
}

func (v mergeVal) equal(w mergeVal) bool { return v.typ == w.typ && v.addr == w.addr }

func mergeValOf(e object.TreeEntry, ok bool) mergeVal {
	if !ok {
		return mergeVal{}
	}
	return mergeVal{typ: e.Type, addr: e.Addr}
}

// mergeNode 是合成树的内存中间表示:
//   - dirty=false:该子树三侧判定后与 ours 逐字节一致,直接复用 store 中
//     已有地址(结构共享,零写入);
//   - dirty=true:tree 持有本层合成条目表(以 ours 为底叠加单侧变更),
//     dirty 的子目录挂在 subs,写树时自底向上解析地址回填(见 writeMerged)。
type mergeNode struct {
	dirty bool
	addr  hash.Address // !dirty 时有效:ours 子树的 store 地址
	tree  *object.Tree // dirty 时有效:合成条目表
	subs  map[string]*mergeNode
}

// mergeOutput 汇总一次三方合并的冲突与自动合并计数。
type mergeOutput struct {
	conflicts  []MergeConflict
	autoMerged int
}

// mergeTrees 对三棵树按全路径逐 slug 比较 (type, addr) 三元组,判定表见调研
// §2.3。纯函数:只读对象,不写任何新对象、不动分支指针、不建索引。
// oursAddr 是 ours 子树在 store 中的地址(dirty=false 时整树复用);
// path 是当前目录全路径组件(用于冲突清单的全路径)。
//
// 逐 slug 判定(值相等指 (type, addr) 全等,⊥ 为空值):
//   - ours == theirs → 双侧一致(行 1 未变 / 行 4 双侧同变含同删),结果取 ours;
//   - theirs == base → ours 单侧变(行 3),结果取 ours;
//   - ours == base   → theirs 单侧变(行 2,含删除),结果取 theirs;
//   - 三方皆目录且互异(行 7)→ 递归下钻;ours/theirs 皆目录而 base 非目录
//     (双侧各自新建目录)→ 以空树为基准递归,子层逐 slug 判定,相同子条目仍自动合;
//   - 其余 → 冲突(行 5 content / 行 6 modify-delete / 行 8 type),
//     合并树中该条目取 ours 占位,清单登记全路径。
//
// 目录递归采用调研 §3.2 演算的合成语义(自底向上只写变化层):冲突条目取
// ours 占位,同目录内非冲突判定照常生效——占位若取「整目录 ours」会静默丢弃
// 冲突目录内 theirs 侧的单侧变更(如 §3.2 中 workflow/daily 的删除),故按
// 条目粒度合成。
func (r *Repo) mergeTrees(ctx context.Context, base, ours, theirs *object.Tree, oursAddr hash.Address, path []string, out *mergeOutput) (*mergeNode, error) {
	slugs := make(map[string]bool, len(ours.Entries))
	for _, e := range base.Entries {
		slugs[e.Slug] = true
	}
	for _, e := range ours.Entries {
		slugs[e.Slug] = true
	}
	for _, e := range theirs.Entries {
		slugs[e.Slug] = true
	}
	ordered := make([]string, 0, len(slugs))
	for s := range slugs {
		ordered = append(ordered, s)
	}
	sort.Strings(ordered) // 确定性遍历(树编码本就按 slug 排序)

	node := &mergeNode{addr: oursAddr, tree: ours.Clone()}
	for _, slug := range ordered {
		b := mergeValOf(base.Lookup(slug))
		o := mergeValOf(ours.Lookup(slug))
		t := mergeValOf(theirs.Lookup(slug))
		switch {
		case o.equal(t):
			// 行 1 未变 / 行 4 双侧同变(含双侧同删):结果=ours,树已就位
			if !o.equal(b) {
				out.autoMerged++
			}
		case t.equal(b):
			// 行 3 ours 单侧变(含 ours 删除):结果=ours
			out.autoMerged++
		case o.equal(b):
			// 行 2 theirs 单侧变(含 theirs 删除):结果=theirs
			if t.typ == "" {
				node.tree.Delete(slug)
			} else {
				node.tree.Set(slug, t.typ, t.addr)
			}
			node.dirty = true
			out.autoMerged++
		case o.typ == object.EntryDir && t.typ == object.EntryDir:
			// 目录双侧异变:递归下钻。base 非目录(双侧各自新建)时以空树
			// 为基准,子层逐 slug 判定(相同子条目同地址自动合,相异按 add/add 冲突)。
			baseSub := object.NewTree()
			if b.typ == object.EntryDir {
				loaded, err := r.loadTree(ctx, b.addr)
				if err != nil {
					return nil, err
				}
				baseSub = loaded
			}
			oursSub, err := r.loadTree(ctx, o.addr)
			if err != nil {
				return nil, err
			}
			theirsSub, err := r.loadTree(ctx, t.addr)
			if err != nil {
				return nil, err
			}
			sub, err := r.mergeTrees(ctx, baseSub, oursSub, theirsSub, o.addr, append(append([]string{}, path...), slug), out)
			if err != nil {
				return nil, err
			}
			if sub.dirty {
				if node.subs == nil {
					node.subs = map[string]*mergeNode{}
				}
				node.subs[slug] = sub
				node.tree.Set(slug, object.EntryDir, "") // 地址待 writeMerged 回填
				node.dirty = true
			}
		default:
			// 双侧异变冲突域(行 5/6/8 + 形态对撞):按方向与形态归类
			out.conflicts = append(out.conflicts, MergeConflict{
				Path:   JoinPath(append(append([]string{}, path...), slug)),
				Kind:   mergeConflictKind(o, t),
				Base:   b.addr,
				Ours:   o.addr,
				Theirs: t.addr,
			})
			// 树中占位取 ours(含 ours 删除=结果不含),与初始树一致,无脏
		}
	}
	return node, nil
}

// mergeConflictKind 归类双侧异变冲突:恰一方是目录而另一方有值 → type
// (note↔dir 对撞);任一方为 ⊥ → modify-delete(删改对撞);其余(双方皆有值、
// 皆非目录)→ content(双侧异改,含 add/add)。
func mergeConflictKind(o, t mergeVal) string {
	oDir := o.typ == object.EntryDir
	tDir := t.typ == object.EntryDir
	switch {
	case oDir != tDir && o.typ != "" && t.typ != "":
		return mergeKindType
	case o.typ == "" || t.typ == "":
		return mergeKindModifyDelete
	default:
		return mergeKindContent
	}
}

// writeMerged 把合成树自底向上落库:未变子树直接复用 store 地址(结构共享,
// 零写入),变更层写新 tree(子层先写,地址回填父层条目)。全程 Put 幂等,
// 失败重试安全。返回根地址与解析完毕的根树。
func (r *Repo) writeMerged(ctx context.Context, n *mergeNode) (hash.Address, *object.Tree, error) {
	if !n.dirty {
		t, err := r.loadTree(ctx, n.addr)
		if err != nil {
			return "", nil, err
		}
		return n.addr, t, nil
	}
	for slug, sub := range n.subs {
		subAddr, _, err := r.writeMerged(ctx, sub)
		if err != nil {
			return "", nil, err
		}
		n.tree.Set(slug, object.EntryDir, subAddr)
	}
	n.subs = nil
	addr, err := r.putTree(ctx, n.tree)
	if err != nil {
		return "", nil, err
	}
	return addr, n.tree, nil
}

// MergeOptions 是合并入参。
type MergeOptions struct {
	// Message 是合并提交消息;空为 "merge theirs"。
	Message string
	// Base 是显式基准(分支名/完整地址/短标识);多 LCA(蟹状历史)时必填,
	// 必须是候选之一(调研 §2.2 方案甲)。
	Base string
}

// MergeResult 汇报一次合并。
type MergeResult struct {
	UpToDate    bool
	FastForward bool
	Transferred int
	Base        hash.Address
	Ours        hash.Address
	Theirs      hash.Address
	Snap        hash.Address    // 零冲突:合并快照地址;冲突/未落库时为空
	Root        hash.Address    // 零冲突:合并树根地址
	AutoMerged  int             // 自动合并条目数(判定表行 2/3/4)
	Conflicts   []MergeConflict // 冲突清单(按路径字典序)

	// mergedNode 是冲突时未落库的合成树内存表示(B 批次中间态落基线快照用;
	// 内核冲突即停、不落任何对象与指针,此字段仅供同包 mergestate.go 消费,
	// 零冲突与外部调用方不感知)。
	mergedNode *mergeNode
}

// Merge 把远端分支合并进当前分支:远端可达对象先传输(只取本地缺失),
// 再按祖先关系分派——已更新 / fast-forward / 三方合并(调研 §2.7 判定矩阵的
// --merge 列)。分叉时:LCA 选基准 → 三方树合并;冲突为空才落库(合并快照
// Parents=[ours, theirs],两条 parent 链都保持可达),非空则全有或全无返回
// *ErrMergeConflicts。合并仅限同项目(v1 口径)。
func (r *Repo) Merge(ctx context.Context, src store.Store, srcProject, srcBranch string, opt MergeOptions) (MergeResult, error) {
	if err := r.rejectIfMerging(ctx, "merge"); err != nil {
		return MergeResult{}, err
	}
	theirsHead, err := src.BranchGet(ctx, srcProject, srcBranch)
	if err != nil {
		return MergeResult{}, fmt.Errorf("repo: 远端分支 %q: %w", srcBranch, err)
	}
	oursHead, hasLocal, err := r.head(ctx)
	if err != nil {
		return MergeResult{}, err
	}
	res := MergeResult{Ours: oursHead, Theirs: theirsHead}
	if hasLocal && oursHead == theirsHead {
		res.UpToDate = true
		return res, nil
	}
	if hasLocal {
		ahead, err := r.isAncestor(ctx, oursHead, theirsHead, true) // theirs ∈ ancestors(ours)
		if err != nil {
			return MergeResult{}, err
		}
		if ahead {
			// 本地已包含远端全部内容:已是最新空操作(§2.7 矩阵修正列)
			res.UpToDate = true
			return res, nil
		}
	}
	tx := &transfer{st: r.st, src: src, seen: map[string]bool{}}
	if err := tx.copy(ctx, theirsHead); err != nil {
		return res, err
	}
	res.Transferred = tx.n
	if !hasLocal {
		// 空库无可合并基准:按 fast-forward 语义推进(与 Pull 空库一致)
		if err := r.st.BranchSet(ctx, r.project, r.branch, theirsHead); err != nil {
			return res, r.translateBranchSetErr(err)
		}
		res.FastForward = true
		return res, nil
	}
	ff, err := r.isAncestor(ctx, theirsHead, oursHead, true) // ours ∈ ancestors(theirs)
	if err != nil {
		return res, err
	}
	if ff {
		if err := r.st.BranchSet(ctx, r.project, r.branch, theirsHead); err != nil {
			return res, r.translateBranchSetErr(err)
		}
		res.FastForward = true
		return res, nil
	}
	// 分叉:三方合并
	mb, err := r.MergeBase(ctx, oursHead, theirsHead, opt.Base)
	if err != nil {
		return res, err
	}
	res.Base = mb.Base
	baseSnap, err := r.loadSnapshot(ctx, mb.Base)
	if err != nil {
		return res, err
	}
	oursSnap, err := r.loadSnapshot(ctx, oursHead)
	if err != nil {
		return res, err
	}
	theirsSnap, err := r.loadSnapshot(ctx, theirsHead)
	if err != nil {
		return res, err
	}
	baseTree, err := r.loadTree(ctx, baseSnap.Root)
	if err != nil {
		return res, err
	}
	oursTree, err := r.loadTree(ctx, oursSnap.Root)
	if err != nil {
		return res, err
	}
	theirsTree, err := r.loadTree(ctx, theirsSnap.Root)
	if err != nil {
		return res, err
	}
	out := &mergeOutput{}
	node, err := r.mergeTrees(ctx, baseTree, oursTree, theirsTree, oursSnap.Root, nil, out)
	if err != nil {
		return res, err
	}
	res.AutoMerged = out.autoMerged
	if len(out.conflicts) > 0 {
		sort.Slice(out.conflicts, func(i, j int) bool { return out.conflicts[i].Path < out.conflicts[j].Path })
		res.Conflicts = out.conflicts
		res.mergedNode = node // 未落库的合成树,B 批次中间态落基线用
		return res, &ErrMergeConflicts{Conflicts: out.conflicts}
	}
	rootAddr, rootTree, err := r.writeMerged(ctx, node)
	if err != nil {
		return res, err
	}
	res.Root = rootAddr
	// 索引增量:与 commit 同路径(旧索引 + 新旧树叶子差集);合并树与 ours 树
	// 一致时零差异,结构共享复用 ours 索引地址
	idxAddr, err := r.updateIndex(ctx, oursSnap.Index, oursTree, rootTree)
	if err != nil {
		return res, err
	}
	msg := opt.Message
	if msg == "" {
		msg = mergeDefaultMessage
	}
	snap := &object.Snapshot{
		Kind:    object.KindSnapshot,
		Root:    rootAddr,
		Parents: []hash.Address{oursHead, theirsHead},
		Time:    r.now(),
		Message: msg,
		Index:   idxAddr,
	}
	snapData, err := object.EncodeSnapshot(snap)
	if err != nil {
		return res, err
	}
	snapAddr, err := r.st.Put(ctx, object.KindSnapshot, snapData)
	if err != nil {
		return res, err
	}
	if err := r.st.BranchSet(ctx, r.project, r.branch, snapAddr); err != nil {
		return res, r.translateBranchSetErr(err)
	}
	res.Snap = snapAddr
	return res, nil
}
