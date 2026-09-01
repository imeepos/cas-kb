package object

import (
	"bytes"
	"strings"
	"testing"
)

func mkAddr(hex string) Address { return Address("sha256:" + hex) }

// M4:索引根规范化编码——文档表按地址排序,重复编码逐字节一致。
func TestEncodeIndexRootCanonical(t *testing.T) {
	ir := &IndexRoot{
		Kind:    KindIndexRoot,
		Version: 1,
		Shards:  make([]Address, 4),
		Docs: []IndexDoc{
			{Addr: mkAddr("bb"), Path: "b", Len: 2},
			{Addr: mkAddr("aa"), Path: "a", Len: 1},
		},
	}
	enc1, err := EncodeIndexRoot(ir)
	if err != nil {
		t.Fatal(err)
	}
	enc2, _ := EncodeIndexRoot(ir)
	if !bytes.Equal(enc1, enc2) {
		t.Fatal("同一对象两次编码应逐字节一致")
	}
	// 文档表应按地址排序:aa 在 bb 之前
	if bytes.Index(enc1, []byte("sha256:bb")) < bytes.Index(enc1, []byte("sha256:aa")) {
		t.Fatal("文档表应按地址排序")
	}
	back, err := DecodeIndexRoot(enc1)
	if err != nil {
		t.Fatal(err)
	}
	if back.Docs[0].Addr != mkAddr("aa") || back.Docs[1].Path != "b" {
		t.Fatalf("往返不符: %+v", back.Docs)
	}
}

// M4:分片规范化编码——词元内倒排项按地址排序。
func TestEncodeIndexShardCanonical(t *testing.T) {
	sh := &IndexShard{
		Kind:   KindIndexShard,
		Bucket: 7,
		Postings: map[string][]IndexPosting{
			"go": {
				{Addr: mkAddr("bb"), Title: 1},
				{Addr: mkAddr("aa"), Title: 2},
			},
		},
	}
	enc, err := EncodeIndexShard(sh)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Index(enc, []byte("sha256:bb")) < bytes.Index(enc, []byte("sha256:aa")) {
		t.Fatal("倒排项应按地址排序")
	}
	back, err := DecodeIndexShard(enc)
	if err != nil {
		t.Fatal(err)
	}
	if back.Postings["go"][0].Addr != mkAddr("aa") {
		t.Fatalf("往返不符: %+v", back.Postings)
	}
}

// M4:快照 index 字段 omitempty——无索引快照编码与 M4 之前逐字节一致。
func TestSnapshotIndexFieldBackwardCompatible(t *testing.T) {
	snap := &Snapshot{Kind: KindSnapshot, Root: mkAddr(strings.Repeat("ab", 32)), Time: 1, Message: "m"}
	enc, err := EncodeSnapshot(snap)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(enc), "\"index\"") {
		t.Fatalf("无索引快照不应出现 index 字段: %s", enc)
	}
	withIdx := &Snapshot{Kind: KindSnapshot, Root: snap.Root, Time: 1, Message: "m", Index: mkAddr(strings.Repeat("cd", 32))}
	enc2, _ := EncodeSnapshot(withIdx)
	if !strings.Contains(string(enc2), "\"index\"") {
		t.Fatalf("带索引快照应含 index 字段: %s", enc2)
	}
	// 旧字节(无 index 字段)解码应成功,Index 为空
	back, err := DecodeSnapshot(enc)
	if err != nil {
		t.Fatal(err)
	}
	if back.Index != "" {
		t.Fatalf("旧快照解码 Index 应为空: %q", back.Index)
	}
}
