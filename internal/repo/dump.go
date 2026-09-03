package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/imeepos/cas-kb/internal/hash"
	"github.com/imeepos/cas-kb/internal/object"
	"github.com/imeepos/cas-kb/internal/store"
)

// ErrNonEmptyLibrary 表示目标库非空,恢复被拒绝(可加 force 覆盖)。
var ErrNonEmptyLibrary = errors.New("repo: 目标库非空")

// MinRestoreSchemaVersion 是可恢复备份的最低来源库版本。
// 对象编码自 v4 起(带类型 tree 条目,M3.8)与当前完全兼容——v5 只放宽
// kind 约束并给 snapshot 加可选 index 字段,v6 再放宽纳入向量对象(同样
// 只是加 kind),v4 备份的每个对象都可原样导入;
// 更早版本(v3 及以下)的 tree 字节无法被当前解码,恢复没有意义,仍拒绝。
const MinRestoreSchemaVersion = 4

// DumpStats 汇报一次备份/恢复的对象、项目、分支计数。
// Restore 时 FromSchemaVersion 为备份来源的库版本,供 CLI 提示升级。
type DumpStats struct {
	Objects           int
	Projects          int
	Branches          int
	FromSchemaVersion int
}

// dumpRecord 是备份文件(JSONL)的一行,按 type 区分:header / object / project / branch。
// data 由 encoding/json 自动 base64;空对象 Data 保持显式字段(不得 omitempty)。
type dumpRecord struct {
	Type          string       `json:"type"`
	SchemaVersion int          `json:"schema_version,omitempty"`
	CreatedAt     int64        `json:"created_at,omitempty"`
	Addr          hash.Address `json:"addr,omitempty"`
	Kind          object.Kind  `json:"kind,omitempty"`
	Size          int          `json:"size,omitempty"`
	Data          []byte       `json:"data"`
	Name          string       `json:"name,omitempty"`
	Description   string       `json:"description,omitempty"`
	Project       string       `json:"project,omitempty"`
}

// DumpLibrary 把整库(对象 + 项目 + 分支)流式导出为 JSONL 备份。
// 备份是全库语义(对象全局共享),不受项目作用域影响。
func DumpLibrary(ctx context.Context, s store.Store, w io.Writer) (DumpStats, error) {
	var stats DumpStats
	enc := json.NewEncoder(w)
	if err := enc.Encode(dumpRecord{Type: "header", SchemaVersion: store.DBSchemaVersion, CreatedAt: time.Now().Unix()}); err != nil {
		return stats, fmt.Errorf("repo: 写备份文件头失败: %w", err)
	}
	if err := s.List(ctx, func(info store.ObjectInfo) error {
		data, kind, err := s.Get(ctx, info.Addr)
		if err != nil {
			return fmt.Errorf("repo: 读取对象 %s: %w", info.Addr, err)
		}
		if kind != info.Kind {
			return fmt.Errorf("repo: 对象 %s 的 kind 不一致(%s != %s)", info.Addr, info.Kind, kind)
		}
		if err := enc.Encode(dumpRecord{Type: "object", Addr: info.Addr, Kind: kind, Size: len(data), Data: data}); err != nil {
			return fmt.Errorf("repo: 写对象 %s: %w", info.Addr, err)
		}
		stats.Objects++
		return nil
	}); err != nil {
		return stats, err
	}
	projects, err := s.ProjectStats(ctx)
	if err != nil {
		return stats, err
	}
	for _, p := range projects {
		if err := enc.Encode(dumpRecord{Type: "project", Name: p.Project, Description: p.Description}); err != nil {
			return stats, fmt.Errorf("repo: 写项目 %s: %w", p.Project, err)
		}
		stats.Projects++
	}
	branches, err := s.BranchListAll(ctx)
	if err != nil {
		return stats, err
	}
	for _, b := range branches {
		if err := enc.Encode(dumpRecord{Type: "branch", Project: b.Project, Name: b.Name, Addr: b.Addr, Description: b.Description}); err != nil {
			return stats, fmt.Errorf("repo: 写分支 %s/%s: %w", b.Project, b.Name, err)
		}
		stats.Branches++
	}
	return stats, nil
}

// RestoreLibrary 从 DumpLibrary 产物恢复整库。
// 导入时逐对象重算哈希校验完整性;目标库非空时拒绝(force=true 则先 Wipe 覆盖)。
// 恢复完成后建议运行 kb fsck 复核。
func RestoreLibrary(ctx context.Context, s store.Store, r io.Reader, force bool) (DumpStats, error) {
	var stats DumpStats

	// 非空检查:有对象或分支即视为非空
	count := 0
	errStop := errors.New("stop")
	if err := s.List(ctx, func(store.ObjectInfo) error { count++; return errStop }); err != nil && !errors.Is(err, errStop) {
		return stats, err
	}
	if count == 0 {
		branches, err := s.BranchListAll(ctx)
		if err != nil {
			return stats, err
		}
		count += len(branches)
	}
	if count > 0 {
		if !force {
			return stats, fmt.Errorf("repo: 目标库非空(拒绝覆盖): %w;确认覆盖请加 --force(将先清空)", ErrNonEmptyLibrary)
		}
		if err := s.Wipe(ctx); err != nil {
			return stats, err
		}
	}

	dec := json.NewDecoder(r)
	var rec dumpRecord
	if err := dec.Decode(&rec); err != nil {
		return stats, fmt.Errorf("repo: 读取备份文件头失败: %w", err)
	}
	if rec.Type != "header" {
		return stats, fmt.Errorf("repo: 不是备份文件(缺少 header 行)")
	}
	// 跨版本恢复门禁:接受 [MinRestoreSchemaVersion, DBSchemaVersion] 区间——
	// 对象编码在窗口内逐字节兼容,旧备份恢复后建议立即重新 backup 完成升级。
	switch {
	case rec.SchemaVersion > store.DBSchemaVersion:
		return stats, fmt.Errorf("repo: 备份来自更新版本的 kb(header schema_version=%d,当前支持 %d);请先升级 kb 再恢复", rec.SchemaVersion, store.DBSchemaVersion)
	case rec.SchemaVersion < MinRestoreSchemaVersion:
		return stats, fmt.Errorf("repo: 备份过旧(header schema_version=%d,最低支持 %d):该版本的对象编码与当前不兼容;请用配套旧版 kb 恢复后重新导出", rec.SchemaVersion, MinRestoreSchemaVersion)
	}
	stats.FromSchemaVersion = rec.SchemaVersion

	// 对象流式导入;项目/分支缓存到最后统一落(分支外键依赖项目)
	var projects, branches []dumpRecord
	for {
		if err := dec.Decode(&rec); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return stats, fmt.Errorf("repo: 备份文件损坏: %w", err)
		}
		switch rec.Type {
		case "object":
			if hash.Sum(rec.Data) != rec.Addr {
				return stats, fmt.Errorf("repo: 对象 %s 校验失败(内容与地址不符,备份可能损坏)", rec.Addr)
			}
			if _, err := s.Put(ctx, rec.Kind, rec.Data); err != nil {
				return stats, err
			}
			stats.Objects++
		case "project":
			projects = append(projects, rec)
		case "branch":
			branches = append(branches, rec)
		default:
			return stats, fmt.Errorf("repo: 备份文件含未知记录类型 %q", rec.Type)
		}
	}
	for _, p := range projects {
		if err := s.ProjectCreate(ctx, p.Name, p.Description); err != nil {
			return stats, err
		}
		stats.Projects++
	}
	for _, b := range branches {
		if err := s.BranchSet(ctx, b.Project, b.Name, b.Addr); err != nil {
			return stats, err
		}
		if err := s.BranchDescribe(ctx, b.Project, b.Name, b.Description); err != nil {
			return stats, err
		}
		stats.Branches++
	}
	return stats, nil
}
