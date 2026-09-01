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

// errStopScan 是扫描的内部提前终止信号:命中第二个快照即可判定歧义。
var errStopScan = errors.New("repo: 终止前缀扫描")

// resolveByPrefix 在全部快照对象中按地址前缀唯一匹配。
// 命中第二个快照即提前终止扫描并报歧义,避免无谓的全表遍历。
func (r *Repo) resolveByPrefix(ctx context.Context, prefix string) (hash.Address, error) {
	var match hash.Address
	n := 0
	err := r.st.List(ctx, func(info store.ObjectInfo) error {
		if info.Kind == object.KindSnapshot && strings.HasPrefix(string(info.Addr), prefix) {
			match = info.Addr
			n++
			if n > 1 {
				return errStopScan
			}
		}
		return nil
	})
	if errors.Is(err, errStopScan) {
		return "", ErrAmbiguousRef
	}
	if err != nil {
		return "", err
	}
	switch n {
	case 1:
		return match, nil
	case 0:
		return "", fmt.Errorf("repo: 引用 %q 既不是分支也不是快照短标识: %w", prefix, store.ErrBranchNotFound)
	default:
		return "", ErrAmbiguousRef
	}
}
