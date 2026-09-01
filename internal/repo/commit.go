package repo

import (
	"context"
	"strings"

	"github.com/imeepos/cas-kb/internal/hash"
	"github.com/imeepos/cas-kb/internal/object"
	"github.com/imeepos/cas-kb/internal/store"
)

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

// Resolve 把"分支名或 sha256 地址"解析为快照地址。
func (r *Repo) Resolve(ctx context.Context, name string) (hash.Address, error) {
	if strings.HasPrefix(name, string(hash.PrefixSha256)) {
		return hash.Address(name), nil
	}
	branches, err := r.st.BranchList(ctx)
	if err != nil {
		return "", err
	}
	for _, b := range branches {
		if b.Name == name {
			return b.Addr, nil
		}
	}
	return "", store.ErrBranchNotFound
}
