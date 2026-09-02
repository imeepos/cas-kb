package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/imeepos/cas-kb/internal/hash"
	"github.com/imeepos/cas-kb/internal/object"
)

// Put 幂等写入对象并返回内容地址。同地址重复写等价于空操作。
// 存储字节经过透明压缩编码(仅索引类,见 compress.go);地址与哈希
// 始终基于逻辑字节,Get 返回解压后的原始内容。
func (s *SQLite) Put(ctx context.Context, kind object.Kind, data []byte) (hash.Address, error) {
	addr := hash.Sum(data)
	if data == nil {
		// nil 会被驱动绑定为 NULL,违反 objects.data NOT NULL;归一为空 blob
		// (空 body blob 在 SQLite 读回时可能呈现为 nil,拉取/恢复路径会再写回)
		data = []byte{}
	}
	stored := encodeObjectData(kind, data)
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO objects (addr, kind, size, data) VALUES (?, ?, ?, ?) ON CONFLICT (addr) DO NOTHING",
		string(addr), string(kind), len(stored), stored)
	if err != nil {
		return "", fmt.Errorf("store: Put 失败: %w", err)
	}
	return addr, nil
}

// Get 读取对象字节与类型;不存在返回 ErrNotFound。
// 返回逻辑字节(索引类对象自动解压),与写入前内容逐字节一致。
func (s *SQLite) Get(ctx context.Context, addr hash.Address) ([]byte, object.Kind, error) {
	var data []byte
	var kind string
	err := s.db.QueryRowContext(ctx,
		"SELECT data, kind FROM objects WHERE addr = ?", string(addr)).Scan(&data, &kind)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", ErrNotFound
	}
	if err != nil {
		return nil, "", fmt.Errorf("store: Get 失败: %w", err)
	}
	logical, err := decodeObjectData(object.Kind(kind), data)
	if err != nil {
		return nil, "", fmt.Errorf("store: Get 解码失败: %w", err)
	}
	return logical, object.Kind(kind), nil
}

// Has 报告对象是否存在。
func (s *SQLite) Has(ctx context.Context, addr hash.Address) (bool, error) {
	var one int
	err := s.db.QueryRowContext(ctx, "SELECT 1 FROM objects WHERE addr = ?", string(addr)).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("store: Has 失败: %w", err)
	}
	return true, nil
}

// Delete 删除单个对象,仅供 GC 使用。不存在的地址视为成功。
func (s *SQLite) Delete(ctx context.Context, addr hash.Address) error {
	if _, err := s.db.ExecContext(ctx, "DELETE FROM objects WHERE addr = ?", string(addr)); err != nil {
		return fmt.Errorf("store: Delete 失败: %w", err)
	}
	return nil
}

// List 全量扫描对象,逐行调用 fn;fn 返回错误则中断扫描。
// fn 内允许嵌套 Get/Delete(GC/FSCK 形态):WAL 读写并发 + 连接池支撑。
func (s *SQLite) List(ctx context.Context, fn func(ObjectInfo) error) error {
	rows, err := s.db.QueryContext(ctx, "SELECT addr, kind, size FROM objects")
	if err != nil {
		return fmt.Errorf("store: List 失败: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var info ObjectInfo
		if err := rows.Scan(&info.Addr, &info.Kind, &info.Size); err != nil {
			return fmt.Errorf("store: List 扫描失败: %w", err)
		}
		if err := fn(info); err != nil {
			return err
		}
	}
	return rows.Err()
}
