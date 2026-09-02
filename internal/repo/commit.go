package repo

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/imeepos/cas-kb/internal/hash"
	"github.com/imeepos/cas-kb/internal/object"
	"github.com/imeepos/cas-kb/internal/store"
)

// ErrAmbiguousRef 表示短标识命中多个快照,无法唯一解析。
var ErrAmbiguousRef = errors.New("repo: 短标识匹配多个快照,请提供更长前缀")

// ErrEntryTypeConflict 表示路径段类型冲突:期望目录/条目,实际是另一类
// (如 "a/b" 中的 a 是条目,或按条目读取的路径其实是目录)。
// 只读 HTTP API 用它把这类客户端路径问题映射为 400(DESIGN §8.5)。
var ErrEntryTypeConflict = errors.New("repo: 路径段类型冲突")

// translateBranchSetErr 把分支推进失败转译为可行动提示。
// note/blob 对象在同一写路径刚写入必然存在,外键失败的现实根因
// 几乎总是「项目未创建」(项目是分支外键的命名空间父级)。
func (r *Repo) translateBranchSetErr(err error) error {
	msg := err.Error()
	if strings.Contains(msg, "FOREIGN KEY") || strings.Contains(msg, "foreign key") {
		return fmt.Errorf("repo: 项目 %q 不存在,请先执行 kb project create %s(%v)", r.project, r.project, err)
	}
	return err
}

// LogEntry 是日志中的一条:快照地址与内容。
type LogEntry struct {
	Addr     hash.Address
	Snapshot *object.Snapshot
}

// Commit 在当前状态上新建一个快照并推进分支(commit 消息落盘)。
func (r *Repo) Commit(ctx context.Context, msg string) (hash.Address, error) {
	t, hasHead, err := r.currentTree(ctx)
	if err != nil {
		return "", err
	}
	return r.commitTree(ctx, t, msg, hasHead)
}

// Log 沿 parents 链从分支头回溯全部快照(最新在前)。
func (r *Repo) Log(ctx context.Context) ([]LogEntry, error) {
	head, has, err := r.head(ctx)
	if err != nil {
		return nil, err
	}
	if !has {
		return []LogEntry{}, nil
	}
	var out []LogEntry
	seen := map[string]bool{}
	cur := head
	for cur != "" && !seen[string(cur)] {
		seen[string(cur)] = true
		snap, err := r.loadSnapshot(ctx, cur)
		if err != nil {
			return nil, err
		}
		out = append(out, LogEntry{Addr: cur, Snapshot: snap})
		if len(snap.Parents) == 0 {
			break
		}
		cur = snap.Parents[0]
	}
	return out, nil
}

// Resolve 把"分支名、完整快照地址或地址短标识"解析为快照地址。
// 短标识即地址前缀(kb log 输出的首列);完整地址原样返回,存在性由读取时校验。
func (r *Repo) Resolve(ctx context.Context, name string) (hash.Address, error) {
	if strings.HasPrefix(name, string(hash.PrefixSha256)) {
		if len(name) >= len(hash.PrefixSha256)+hash.HexLen {
			return hash.Address(name), nil
		}
		return r.resolveByPrefix(ctx, name)
	}
	branches, err := r.st.BranchList(ctx, r.project)
	if err != nil {
		return "", err
	}
	for _, b := range branches {
		if b.Name == name {
			return b.Addr, nil
		}
	}
	return r.resolveByPrefix(ctx, name)
}

// reachableSnapshots 从当前项目全部分支头沿 parents 收集可达快照地址集合。
func (r *Repo) reachableSnapshots(ctx context.Context) (map[string]bool, error) {
	branches, err := r.st.BranchList(ctx, r.project)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	queue := make([]hash.Address, 0, len(branches))
	for _, b := range branches {
		queue = append(queue, b.Addr)
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if seen[string(cur)] {
			continue
		}
		seen[string(cur)] = true
		snap, err := r.loadSnapshot(ctx, cur)
		if err != nil {
			return nil, err
		}
		queue = append(queue, snap.Parents...)
	}
	return seen, nil
}

// resolveByPrefix 在当前项目的可达快照集合内按地址前缀唯一匹配。
// 集合外的快照(他项目)对解析不可见——项目隔离的一部分。
func (r *Repo) resolveByPrefix(ctx context.Context, prefix string) (hash.Address, error) {
	set, err := r.reachableSnapshots(ctx)
	if err != nil {
		return "", err
	}
	match := hash.Address("")
	n := 0
	for addr := range set {
		if strings.HasPrefix(addr, prefix) {
			match = hash.Address(addr)
			n++
		}
	}
	switch n {
	case 1:
		return match, nil
	case 0:
		return "", fmt.Errorf("repo: 引用 %q 既不是本项目的分支也不是快照短标识: %w", prefix, store.ErrBranchNotFound)
	default:
		return "", ErrAmbiguousRef
	}
}
