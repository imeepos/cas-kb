package object

import (
	"bytes"
	"testing"
)

func sampleNote() *Note {
	return &Note{
		Kind:  KindNote,
		Meta:  Meta{Title: "标题", Tags: []string{"z", "a", "m"}, CreatedAt: 1700000000, SchemaVersion: SchemaVersion},
		Body:  Sum([]byte("body")),
		Links: []Link{{Slug: "b", Rel: "related"}, {Slug: "a"}},
	}
}

func TestCanonicalEncodingStable(t *testing.T) {
	n := sampleNote()
	b1, err := EncodeNote(n)
	if err != nil {
		t.Fatal(err)
	}
	b2, err := EncodeNote(n)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b1, b2) {
		t.Fatalf("同一逻辑对象编码两次字节不一致")
	}
	a1, _ := HashOf(KindNote, n)
	a2, _ := HashOf(KindNote, n)
	if a1 != a2 {
		t.Fatalf("同一逻辑对象地址不一致: %s != %s", a1, a2)
	}
}

func TestEncodeSortsTagsAndLinks(t *testing.T) {
	n := sampleNote()
	b, err := EncodeNote(n)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := DecodeNote(b)
	if err != nil {
		t.Fatal(err)
	}
	wantTags := []string{"a", "m", "z"}
	for i, tg := range dec.Meta.Tags {
		if tg != wantTags[i] {
			t.Fatalf("tags 未按字典序: %v", dec.Meta.Tags)
		}
	}
	if dec.Links[0].Slug != "a" || dec.Links[1].Slug != "b" {
		t.Fatalf("links 未按 slug 排序: %v", dec.Links)
	}
}

func TestNoteRoundtrip(t *testing.T) {
	n := sampleNote()
	b, _ := EncodeNote(n)
	dec, err := DecodeNote(b)
	if err != nil {
		t.Fatal(err)
	}
	if dec.Meta.Title != n.Meta.Title || dec.Meta.CreatedAt != n.Meta.CreatedAt || dec.Body != n.Body {
		t.Fatal("roundtrip 字段不一致")
	}
}

func TestDecodeRejectsBadKind(t *testing.T) {
	raw := []byte("{\"kind\":\"tree\",\"entries\":[]}")
	if _, err := DecodeNote(raw); err == nil {
		t.Fatal("kind 不匹配应报错")
	}
}

func TestDecodeRejectsBadSchema(t *testing.T) {
	n := sampleNote()
	n.Meta.SchemaVersion = SchemaVersion + 1
	b, _ := EncodeNote(n)
	if _, err := DecodeNote(b); err == nil {
		t.Fatal("schema_version 不匹配应报错")
	}
}

func TestTreeAndSnapshotRoundtrip(t *testing.T) {
	tr := &Tree{Kind: KindTree, Entries: []TreeEntry{{Slug: "a", Addr: Sum([]byte("a"))}, {Slug: "b", Addr: Sum([]byte("b"))}}}
	bt, _ := EncodeTree(tr)
	tr2, err := DecodeTree(bt)
	if err != nil || len(tr2.Entries) != 2 {
		t.Fatalf("tree roundtrip 失败: %v", err)
	}
	sp := &Snapshot{Kind: KindSnapshot, Root: Sum([]byte("root")), Parents: []Address{Sum([]byte("p1"))}, Time: 1, Message: "m"}
	bs, _ := EncodeSnapshot(sp)
	sp2, err := DecodeSnapshot(bs)
	if err != nil || sp2.Root != sp.Root || sp2.Message != "m" {
		t.Fatalf("snapshot roundtrip 失败: %v", err)
	}
}

func TestValidateErrors(t *testing.T) {
	n := sampleNote()
	n.Meta.Title = ""
	if err := ValidateNote(n); err == nil {
		t.Fatal("缺标题应报错")
	}
	tr := &Tree{Kind: KindTree, Entries: []TreeEntry{{Slug: "a", Addr: Sum([]byte("x"))}, {Slug: "a", Addr: Sum([]byte("y"))}}}
	if err := ValidateTree(tr); err == nil {
		t.Fatal("重复 slug 应报错")
	}
}
