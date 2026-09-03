package object

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"math"
	"strings"
	"testing"
)

// M6-A:vecshard 规范编码确定性——同输入两次编码逐字节一致、同地址;
// items 按路径排序;字段定序(kind, model, dim, items)。
func TestVectorVecShardCanonicalEncoding(t *testing.T) {
	vec := EncodeVecBase64([]float32{0.5, -1.25, 0, 3.75})
	sh := &VecShard{
		Kind:  KindVecShard,
		Model: "nomic-embed-text",
		Dim:   4,
		Items: []VecItem{
			{Path: "go/zzz", Vec: vec},
			{Path: "go/aaa", Vec: vec},
		},
	}
	enc1, err := EncodeVecShard(sh)
	if err != nil {
		t.Fatal(err)
	}
	enc2, err := EncodeVecShard(sh)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(enc1, enc2) {
		t.Fatalf("同一输入两次编码应逐字节一致:\n%s\n%s", enc1, enc2)
	}
	addr1, err := HashOf(KindVecShard, sh)
	if err != nil {
		t.Fatal(err)
	}
	addr2, _ := HashOf(KindVecShard, sh)
	if addr1 != addr2 {
		t.Fatal("同输入应产出相同地址")
	}
	// items 按路径排序:aaa 在 zzz 之前
	if bytes.Index(enc1, []byte("go/zzz")) < bytes.Index(enc1, []byte("go/aaa")) {
		t.Fatalf("items 应按路径排序: %s", enc1)
	}
	// 字段定序:kind < model < dim < items
	iKind := bytes.Index(enc1, []byte(`"kind":"vecshard"`))
	iModel := bytes.Index(enc1, []byte(`"model":"nomic-embed-text"`))
	iDim := bytes.Index(enc1, []byte(`"dim":4`))
	iItems := bytes.Index(enc1, []byte(`"items":[`))
	if !(iKind < iModel && iModel < iDim && iDim < iItems) {
		t.Fatalf("字段定序应为 kind,model,dim,items: %s", enc1)
	}
	back, err := DecodeVecShard(enc1)
	if err != nil {
		t.Fatal(err)
	}
	if back.Model != sh.Model || back.Dim != sh.Dim || len(back.Items) != 2 {
		t.Fatalf("往返不符: %+v", back)
	}
	if back.Items[0].Path != "go/aaa" {
		t.Fatalf("解码后 items 应保持路径序: %+v", back.Items)
	}
	got, err := DecodeVecBase64(back.Items[0].Vec)
	if err != nil {
		t.Fatal(err)
	}
	want := []float32{0.5, -1.25, 0, 3.75}
	for i := range want {
		if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
			t.Fatalf("向量往返应逐位一致: got=%v want=%v", got, want)
		}
	}
}

// M6-A:float32 小端 base64——与手工构造的 little-endian 字节逐字节一致。
func TestVectorVecBase64LittleEndian(t *testing.T) {
	vals := []float32{1.0, -2.5, 0.0, math.MaxFloat32, 1.5e-38}
	got := EncodeVecBase64(vals)
	// 手工:全部分量按 little-endian 拼接再 base64
	raw := make([]byte, 4*len(vals))
	for i, f := range vals {
		binary.LittleEndian.PutUint32(raw[i*4:], math.Float32bits(f))
	}
	if want := base64.StdEncoding.EncodeToString(raw); got != want {
		t.Fatalf("应与 little-endian 拼接的 base64 一致: got=%s want=%s", got, want)
	}
	// 首分量 1.0 的位型 0x3F800000,little-endian 字节序为 00 00 80 3F
	if b, err := base64.StdEncoding.DecodeString(got); err != nil ||
		!bytes.Equal(b[:4], []byte{0x00, 0x00, 0x80, 0x3F}) {
		t.Fatalf("float32(1.0) 小端字节应为 00 00 80 3F: %v %v", b[:4], err)
	}
	// 非 4 倍长度的 base64 内容必须拒绝
	if _, err := DecodeVecBase64(base64.StdEncoding.EncodeToString([]byte{0x01, 0x02, 0x03})); err == nil {
		t.Fatal("字节长度非 4 的倍数应报错")
	}
}

// M6-A:跨模型(与跨维度)不同址——model/dim 内嵌于内容,换模型必换地址。
func TestVectorCrossModelDifferentAddr(t *testing.T) {
	mk := func(model string, dim int) *VecShard {
		return &VecShard{
			Kind: KindVecShard, Model: model, Dim: dim,
			Items: []VecItem{{Path: "a", Vec: EncodeVecBase64([]float32{0.1, 0.2})}},
		}
	}
	addrA, err := HashOf(KindVecShard, mk("model-a", 2))
	if err != nil {
		t.Fatal(err)
	}
	addrA2, _ := HashOf(KindVecShard, mk("model-a", 2))
	addrB, _ := HashOf(KindVecShard, mk("model-b", 2))
	addrDim, _ := HashOf(KindVecShard, mk("model-a", 768))
	if addrA != addrA2 {
		t.Fatal("同模型同内容应同址")
	}
	if addrA == addrB {
		t.Fatal("跨模型必须不同址")
	}
	if addrA == addrDim {
		t.Fatal("同模型不同维度必须不同址")
	}
}

// M6-A:快照 vec 字段 omitempty——无向量快照编码与之前逐字节一致(无 "vec" 键)。
func TestVectorSnapshotVecOmitEmpty(t *testing.T) {
	base := &Snapshot{Kind: KindSnapshot, Root: mkAddr(strings.Repeat("ab", 32)), Time: 1, Message: "m"}
	enc, err := EncodeSnapshot(base)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(enc), "\"vec\"") {
		t.Fatalf("无向量快照不应出现 vec 字段: %s", enc)
	}
	back, err := DecodeSnapshot(enc)
	if err != nil {
		t.Fatal(err)
	}
	if back.Vec != "" {
		t.Fatalf("旧快照解码 Vec 应为空: %q", back.Vec)
	}
	withVec := &Snapshot{Kind: KindSnapshot, Root: base.Root, Time: 1, Message: "m",
		Vec: mkAddr(strings.Repeat("cd", 32))}
	enc2, _ := EncodeSnapshot(withVec)
	if !strings.Contains(string(enc2), "\"vec\"") {
		t.Fatalf("带向量快照应含 vec 字段: %s", enc2)
	}
	back2, err := DecodeSnapshot(enc2)
	if err != nil {
		t.Fatal(err)
	}
	if back2.Vec != withVec.Vec {
		t.Fatalf("vec 往返不符: %q", back2.Vec)
	}
}

// M6-A:v6 解码门禁——vecshard/vecroot 拒绝不认识/不匹配的编码(同 M4 先例)。
func TestVectorDecodeRejectsWrongKind(t *testing.T) {
	// 载荷 kind 标签不匹配
	foreign, err := EncodeIndexShard(&IndexShard{Kind: KindIndexShard, Bucket: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeVecShard(foreign); err == nil {
		t.Fatal("indexshard 载荷不应能按 vecshard 解码")
	}
	ir, err := EncodeIndexRoot(&IndexRoot{Kind: KindIndexRoot, Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeVecRoot(ir); err == nil {
		t.Fatal("indexroot 载荷不应能按 vecroot 解码")
	}
	// vecshard 自身编码可以按 kind 泛型入口解码
	sh := &VecShard{Kind: KindVecShard, Model: "m", Dim: 2, Items: []VecItem{{Path: "p", Vec: EncodeVecBase64([]float32{0.1, 0.2})}}}
	enc, err := EncodeVecShard(sh)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decode(KindVecShard, enc)
	if err != nil {
		t.Fatal(err)
	}
	if got.(*VecShard).Model != "m" {
		t.Fatalf("泛型解码往返不符: %+v", got)
	}
	if !IsValidKind(KindVecShard) || !IsValidKind(KindVecRoot) {
		t.Fatal("vecshard/vecroot 应是合法 kind")
	}
}

// M6-A:vecroot 规范编码确定性 + 槽位表往返。
func TestVectorVecRootCanonicalEncoding(t *testing.T) {
	vr := &VecRoot{
		Kind:   KindVecRoot,
		Model:  "nomic-embed-text",
		Dim:    4,
		Shards: make([]Address, 8),
	}
	vr.Shards[3] = mkAddr(strings.Repeat("11", 32))
	enc1, err := EncodeVecRoot(vr)
	if err != nil {
		t.Fatal(err)
	}
	enc2, _ := EncodeVecRoot(vr)
	if !bytes.Equal(enc1, enc2) {
		t.Fatalf("同一输入两次编码应逐字节一致:\n%s\n%s", enc1, enc2)
	}
	back, err := DecodeVecRoot(enc1)
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Shards) != 8 || back.Shards[3] != vr.Shards[3] || back.Shards[0] != "" {
		t.Fatalf("vecroot 往返不符: %+v", back)
	}
}
