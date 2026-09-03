package repo

import (
	"context"
	"errors"
	"hash/fnv"
	"strings"
	"testing"

	"github.com/imeepos/cas-kb/internal/hash"
	"github.com/imeepos/cas-kb/internal/object"
)

// fakeEmbedder 是确定性假嵌入器:向量由(文本, 分量下标)哈希派生,
// 同输入恒同输出,跨模型以 model 区分;不依赖网络。
type fakeEmbedder struct {
	model string
	dim   int
	fail  error // 非 nil 时 Embed 响亮失败
	calls int   // Embed 调用计数(断言分批行为)
}

func (f *fakeEmbedder) Model() string { return f.model }
func (f *fakeEmbedder) Dim() int      { return f.dim }

func (f *fakeEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	f.calls++
	if f.fail != nil {
		return nil, f.fail
	}
	out := make([][]float32, len(texts))
	for i, t := range texts {
		v := make([]float32, f.dim)
		for j := range v {
			h := fnv.New32a()
			_, _ = h.Write([]byte(t))
			_, _ = h.Write([]byte{byte(j)})
			v[j] = float32(h.Sum32()%1000) / 1000
		}
		out[i] = v
	}
	return out, nil
}

// fakeVecOf 重算假嵌入器对 text 的向量(测试侧独立复核)。
func fakeVecOf(model string, dim int, text string) []float32 {
	f := &fakeEmbedder{model: model, dim: dim}
	v, _ := f.Embed(context.Background(), []string{text})
	return v[0]
}

// headVecRoot 载入当前头快照的 vecroot(无 vec 则 fatal)。
func headVecRoot(t *testing.T, r *Repo) (*object.Snapshot, *object.VecRoot) {
	t.Helper()
	ctx := context.Background()
	head, has, err := r.head(ctx)
	if err != nil || !has {
		t.Fatalf("应有分支头: %v %v", has, err)
	}
	snap, err := r.loadSnapshot(ctx, head)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Vec == "" {
		t.Fatal("快照应携带向量根地址(vec 字段)")
	}
	root, err := r.LoadVecRoot(ctx, snap.Vec)
	if err != nil {
		t.Fatal(err)
	}
	return snap, root
}

// M6-A:rebuild roundtrip——逐条笔记(标题+正文)嵌入,按 FNV-1a(路径)
// 分桶聚合;items 向量可完整还原且与嵌入器输出逐位一致;快照带 vec 落库。
func TestVectorRebuildRoundtrip(t *testing.T) {
	ctx := context.Background()
	r, _, _ := newRepo(t, "vec_roundtrip")
	emb := &fakeEmbedder{model: "fake-embed", dim: 8}
	for _, p := range []string{"go/channel", "go/context", "misc/notes"} {
		if _, _, err := r.SetNote(ctx, p, NoteInput{Title: "标题 " + p, Body: "正文 " + p}, "add "+p); err != nil {
			t.Fatal(err)
		}
	}
	snapAddr1, vecAddr1, err := r.RebuildEmbeddings(ctx, emb, "embed rebuild")
	if err != nil {
		t.Fatal(err)
	}
	snap, root := headVecRoot(t, r)
	if root.Model != "fake-embed" || root.Dim != 8 {
		t.Fatalf("vecroot model/dim 不符: %q %d", root.Model, root.Dim)
	}
	if len(root.Shards) != VecShardCount {
		t.Fatalf("槽位数应为 %d: %d", VecShardCount, len(root.Shards))
	}
	// 全部三条笔记的向量可还原且逐位一致;路径落在正确桶
	got := map[string][]float32{}
	for bucket, addr := range root.Shards {
		if addr == "" {
			continue
		}
		sh, err := r.LoadVecShard(ctx, addr)
		if err != nil {
			t.Fatal(err)
		}
		if sh.Model != root.Model || sh.Dim != root.Dim {
			t.Fatalf("分片 model/dim 与根不一致: %+v", sh)
		}
		for _, item := range sh.Items {
			if VecBucketOf(item.Path) != bucket {
				t.Fatalf("路径 %q 应在桶 %d,却出现在桶 %d", item.Path, VecBucketOf(item.Path), bucket)
			}
			vec, err := object.DecodeVecBase64(item.Vec)
			if err != nil {
				t.Fatal(err)
			}
			got[item.Path] = vec
		}
	}
	if len(got) != 3 {
		t.Fatalf("应还原 3 条笔记向量: %d", len(got))
	}
	for path, vec := range got {
		// 嵌入输入 = 标题 + 空行 + 正文
		want := fakeVecOf("fake-embed", 8, "标题 "+path+"\n\n正文 "+path)
		for i := range want {
			if vec[i] != want[i] {
				t.Fatalf("路径 %q 向量第 %d 分量不符: %v vs %v", path, i, vec[i], want[i])
			}
		}
	}
	// 幂等:同一输入重复重建,vecroot 地址不变(新快照推进分支)
	snapAddr2, vecAddr2, err := r.RebuildEmbeddings(ctx, emb, "embed rebuild")
	if err != nil {
		t.Fatal(err)
	}
	if vecAddr1 != vecAddr2 {
		t.Fatalf("同输入重建应产出相同 vecroot 地址: %s vs %s", vecAddr1, vecAddr2)
	}
	if snapAddr1 == snapAddr2 {
		t.Fatal("两次重建应产出不同快照(parents 链推进)")
	}
	if snap.Parents == nil || len(snap.Parents) != 1 {
		t.Fatalf("重建快照应携带父母: %+v", snap)
	}
}

// M6-A:跨模型不同址——同一批笔记换模型重建,vecroot/vecshard 地址全变。
func TestVectorRebuildCrossModelDifferentAddr(t *testing.T) {
	ctx := context.Background()
	r, _, _ := newRepo(t, "vec_crossmodel")
	if _, _, err := r.SetNote(ctx, "a", NoteInput{Title: "A", Body: "a"}, "add a"); err != nil {
		t.Fatal(err)
	}
	_, v1, err := r.RebuildEmbeddings(ctx, &fakeEmbedder{model: "model-a", dim: 8}, "m1")
	if err != nil {
		t.Fatal(err)
	}
	_, v2, err := r.RebuildEmbeddings(ctx, &fakeEmbedder{model: "model-b", dim: 8}, "m2")
	if err != nil {
		t.Fatal(err)
	}
	if v1 == v2 {
		t.Fatalf("跨模型重建必须产出不同 vecroot 地址: %s", v1)
	}
	// 旧模型对象仍留在库中(旧快照可达),新旧并存不冲突
	for _, a := range []hash.Address{v1, v2} {
		if ok, err := r.st.Has(ctx, a); err != nil || !ok {
			t.Fatalf("vecroot %s 应存在: %v", a, err)
		}
	}
}

// M6-A:嵌入失败响亮中止——分支指针不动(可安全重试),错误含路径上下文。
func TestVectorRebuildEmbedFailureAbortsLoudly(t *testing.T) {
	ctx := context.Background()
	r, _, _ := newRepo(t, "vec_fail")
	if _, _, err := r.SetNote(ctx, "a", NoteInput{Title: "A", Body: "a"}, "add a"); err != nil {
		t.Fatal(err)
	}
	headBefore, _, err := r.head(ctx)
	if err != nil {
		t.Fatal(err)
	}
	boom := errors.New("嵌入服务不可用")
	_, _, err = r.RebuildEmbeddings(ctx, &fakeEmbedder{model: "m", dim: 4, fail: boom}, "m")
	if err == nil || !strings.Contains(err.Error(), "嵌入") || !strings.Contains(err.Error(), "路径 a") {
		t.Fatalf("嵌入失败应响亮报错并含路径上下文: %v", err)
	}
	headAfter, _, err := r.head(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if headBefore != headAfter {
		t.Fatal("嵌入失败后分支指针不应移动(幂等可重试)")
	}
	// 换正常嵌入器重试即成功(对象幂等)
	if _, _, err := r.RebuildEmbeddings(ctx, &fakeEmbedder{model: "m", dim: 4}, "m"); err != nil {
		t.Fatalf("重试应成功: %v", err)
	}
}

// M6-A:索引共存——rebuild --embed 沿用头快照 BM25 索引;普通 index rebuild
// 反向沿用 vec;内容变更提交不带 vec(旧向量不再描述新内容)。
func TestVectorRebuildCoexistsWithBM25(t *testing.T) {
	ctx := context.Background()
	r, _, _ := newRepo(t, "vec_coexist")
	if _, _, err := r.SetNote(ctx, "go/channel", NoteInput{Title: "Channel", Body: "chan"}, "add"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.RebuildEmbeddings(ctx, &fakeEmbedder{model: "m", dim: 4}, "embed"); err != nil {
		t.Fatal(err)
	}
	snap, _ := headVecRoot(t, r)
	bm25Before := snap.Index
	if bm25Before == "" {
		t.Fatal("rebuild --embed 应沿用头快照的 BM25 索引地址")
	}
	if _, _, err := r.RebuildIndex(ctx, "index rebuild"); err != nil {
		t.Fatal(err)
	}
	snap2, _ := headVecRoot(t, r)
	if snap2.Index != bm25Before {
		t.Fatalf("普通 rebuild 不应改动 BM25 地址: %s vs %s", snap2.Index, bm25Before)
	}
	if snap2.Vec != snap.Vec {
		t.Fatalf("普通 index rebuild 应沿用 vec 地址: %q vs %q", snap2.Vec, snap.Vec)
	}
	// 内容变更:新快照不带 vec(fsck 对无 vec 快照跳过向量校验)
	if _, _, err := r.SetNote(ctx, "new", NoteInput{Title: "N", Body: "n"}, "add new"); err != nil {
		t.Fatal(err)
	}
	head, _, _ := r.head(ctx)
	snap3, err := r.loadSnapshot(ctx, head)
	if err != nil {
		t.Fatal(err)
	}
	if snap3.Vec != "" {
		t.Fatalf("内容变更提交不应携带旧向量: %q", snap3.Vec)
	}
	res, err := r.FSCK(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Problems) != 0 {
		t.Fatalf("fsck 应零问题(无 vec 快照跳过向量校验): %+v", res.Problems)
	}
}

// vecAddrOf 已并入断言(直接比较快照携带的 vec 地址)。

// M6-A:fsck 向量一致性——正常库零问题;脏 vec(路径缺失/model 混版)报 fail。
func TestVectorFSCKConsistency(t *testing.T) {
	ctx := context.Background()
	r, s, _ := newRepo(t, "vec_fsck")
	if _, _, err := r.SetNote(ctx, "go/a", NoteInput{Title: "A", Body: "a"}, "add a"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.SetNote(ctx, "go/b", NoteInput{Title: "B", Body: "b"}, "add b"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.RebuildEmbeddings(ctx, &fakeEmbedder{model: "m", dim: 4}, "embed"); err != nil {
		t.Fatal(err)
	}
	res, err := r.FSCK(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Problems) != 0 {
		t.Fatalf("向量重建后 fsck 应零问题: %+v", res.Problems)
	}

	// 脏向量快照一:items 路径不存在于快照(缺失=fail)
	head, _, _ := r.head(ctx)
	headSnap, _ := r.loadSnapshot(ctx, head)
	// mkVec 造一个向量族:分片 model 与根 model 可分别指定(混版即脏)
	mkVec := func(shardModel, rootModel string) hash.Address {
		items := []object.VecItem{{Path: "ghost/path", Vec: object.EncodeVecBase64([]float32{0.1, 0.2, 0.3, 0.4})}}
		sh := &object.VecShard{Kind: object.KindVecShard, Model: shardModel, Dim: 4, Items: items}
		data, _ := object.EncodeVecShard(sh)
		shardAddr, err := s.Put(ctx, object.KindVecShard, data)
		if err != nil {
			t.Fatal(err)
		}
		shards := make([]hash.Address, VecShardCount)
		shards[VecBucketOf("ghost/path")] = shardAddr
		vr := &object.VecRoot{Kind: object.KindVecRoot, Model: rootModel, Dim: 4, Shards: shards}
		vdata, _ := object.EncodeVecRoot(vr)
		addr, err := s.Put(ctx, object.KindVecRoot, vdata)
		if err != nil {
			t.Fatal(err)
		}
		return addr
	}
	putDirtySnap := func(vecAddr hash.Address, msg string) {
		snap := &object.Snapshot{
			Kind: object.KindSnapshot, Root: headSnap.Root,
			Parents: []hash.Address{head}, Time: fixedTime, Message: msg, Vec: vecAddr,
		}
		data, err := object.EncodeSnapshot(snap)
		if err != nil {
			t.Fatal(err)
		}
		snapAddr, err := s.Put(ctx, object.KindSnapshot, data)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.BranchSet(ctx, r.project, r.branch, snapAddr); err != nil {
			t.Fatal(err)
		}
	}
	putDirtySnap(mkVec("m", "m"), "dirty-missing-path")
	res, err = r.FSCK(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range res.Problems {
		if strings.Contains(p.Problem, "不存在于快照") && strings.Contains(p.Problem, "ghost/path") {
			found = true
		}
	}
	if !found {
		t.Fatalf("路径缺失应报 fail: %+v", res.Problems)
	}

	// 脏向量快照二:分片 model 与根不一致(跨模型混版)
	putDirtySnap(mkVec("other-model", "m"), "dirty-model-mix")
	res, err = r.FSCK(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found = false
	for _, p := range res.Problems {
		if strings.Contains(p.Problem, "不一致") && strings.Contains(p.Problem, "other-model") {
			found = true
		}
	}
	if !found {
		t.Fatalf("model 混版应报 fail: %+v", res.Problems)
	}
}

// M6-A:GC 同规则——vecroot/vecshard 随快照可达保留;悬垂向量对象被清扫;
// gc.keep_last 水位对历史快照的向量与 BM25 索引一视同仁地精简。
func TestVectorGCSweepsAndKeeps(t *testing.T) {
	ctx := context.Background()
	r, s, _ := newRepo(t, "vec_gc")
	if _, _, err := r.SetNote(ctx, "a", NoteInput{Title: "A", Body: "a"}, "add a"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.RebuildEmbeddings(ctx, &fakeEmbedder{model: "m1", dim: 4}, "embed1"); err != nil {
		t.Fatal(err)
	}
	snap1, root1 := headVecRoot(t, r)

	// 悬垂对象:未被任何快照引用的 vecshard/vecroot
	strayShard := &object.VecShard{Kind: object.KindVecShard, Model: "m1", Dim: 4,
		Items: []object.VecItem{{Path: "stray", Vec: object.EncodeVecBase64([]float32{0.1, 0.2, 0.3, 0.4})}}}
	strayData, _ := object.EncodeVecShard(strayShard)
	strayShardAddr, err := s.Put(ctx, object.KindVecShard, strayData)
	if err != nil {
		t.Fatal(err)
	}
	strayRoot := &object.VecRoot{Kind: object.KindVecRoot, Model: "m1", Dim: 4, Shards: make([]hash.Address, VecShardCount)}
	strayRootData, _ := object.EncodeVecRoot(strayRoot)
	strayRootAddr, err := s.Put(ctx, object.KindVecRoot, strayRootData)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := r.UnreachableCount(ctx); err != nil || n < 2 {
		t.Fatalf("悬垂向量对象应计入未达统计: n=%d err=%v", n, err)
	}
	if _, err := r.GC(ctx); err != nil {
		t.Fatal(err)
	}
	for _, a := range []hash.Address{strayShardAddr, strayRootAddr} {
		if ok, _ := s.Has(ctx, a); ok {
			t.Fatalf("悬垂向量对象应被清扫: %s", a)
		}
	}
	// 可达向量随快照保留
	if _, err := r.LoadVecRoot(ctx, snap1.Vec); err != nil {
		t.Fatalf("GC 后可达 vecroot 应保留: %v", err)
	}
	res, err := r.FSCK(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Problems) != 0 {
		t.Fatalf("GC 后 fsck 应零问题: %+v", res.Problems)
	}

	// keep_last 精简:再落两个快照后水位 2,最老快照的向量与 BM25 同被清扫。
	// 第二代向量换模型(m2):跨模型必不同址,两代分片零共享,
	// 旧分片是否清扫的断言不受内容寻址去重干扰。
	if _, _, err := r.SetNote(ctx, "b", NoteInput{Title: "B", Body: "b"}, "add b"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.RebuildEmbeddings(ctx, &fakeEmbedder{model: "m2", dim: 4}, "embed2"); err != nil {
		t.Fatal(err)
	}
	snap2, _ := headVecRoot(t, r)
	if snap2.Vec == snap1.Vec {
		t.Fatal("内容变更后重建应产出新向量族")
	}
	if _, err := r.GCWithKeepLast(ctx, 2); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.Has(ctx, snap1.Vec); ok {
		t.Fatalf("水位外旧快照的 vecroot 应被清扫: %s", snap1.Vec)
	}
	if _, err := r.LoadVecRoot(ctx, snap2.Vec); err != nil {
		t.Fatalf("水位内最新快照的 vecroot 应保留: %v", err)
	}
	// 旧分片同样被清扫(取 root1 的首个非空分片)
	for _, a := range root1.Shards {
		if a == "" {
			continue
		}
		if ok, _ := s.Has(ctx, a); ok {
			t.Fatalf("水位外旧快照的 vecshard 应被清扫: %s", a)
		}
		break
	}
	res, err = r.FSCK(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Problems) != 0 {
		t.Fatalf("keep_last 精简后 fsck 应零问题(豁免口径覆盖 vec): %+v", res.Problems)
	}
}
