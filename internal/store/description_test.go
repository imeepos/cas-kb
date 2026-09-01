package store

import (
	"context"
	"errors"
	"testing"

	"github.com/imeepos/cas-kb/internal/hash"
	"github.com/imeepos/cas-kb/internal/object"
)

// M3.7:项目描述生命周期——建项目带描述、Stats 可见、就地更新、不存在报错。
func TestM37_ProjectDescriptionLifecycle(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	if err := s.ProjectCreate(ctx, "proj", "存 Go 工程踩坑笔记"); err != nil {
		t.Fatal(err)
	}
	st := statOf(t, s, "proj")
	if st.Description != "存 Go 工程踩坑笔记" || st.Branches != 0 {
		t.Fatalf("描述/分支数不符: %+v", st)
	}
	if err := s.ProjectDescribe(ctx, "proj", "改为:Go 工程与部署笔记"); err != nil {
		t.Fatal(err)
	}
	if got := statOf(t, s, "proj").Description; got != "改为:Go 工程与部署笔记" {
		t.Fatalf("描述未更新: %q", got)
	}
	if err := s.ProjectDescribe(ctx, "ghost", "x"); !errors.Is(err, ErrProjectNotFound) {
		t.Fatalf("不存在项目应返回 ErrProjectNotFound,得到 %v", err)
	}
}

// M3.7:分支描述——可写可读,分支推进(UPSERT)不清空描述。
func TestM37_BranchDescriptionSurvivesAdvance(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	if err := s.ProjectCreate(ctx, "p1", ""); err != nil {
		t.Fatal(err)
	}
	a1, err := s.Put(ctx, object.KindBlob, []byte("v1"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.BranchSet(ctx, "p1", "main", a1); err != nil {
		t.Fatal(err)
	}
	if err := s.BranchDescribe(ctx, "p1", "main", "工作线"); err != nil {
		t.Fatal(err)
	}
	refs, err := s.BranchList(ctx, "p1")
	if err != nil || len(refs) != 1 {
		t.Fatalf("BranchList: %v %v", refs, err)
	}
	if refs[0].Description != "工作线" {
		t.Fatalf("描述不符: %+v", refs[0])
	}
	a2, _ := s.Put(ctx, object.KindBlob, []byte("v2"))
	if err := s.BranchSet(ctx, "p1", "main", a2); err != nil {
		t.Fatal(err)
	}
	refs, err = s.BranchList(ctx, "p1")
	if err != nil {
		t.Fatal(err)
	}
	if refs[0].Description != "工作线" {
		t.Fatalf("推进不应清空描述: %+v", refs[0])
	}
	if refs[0].Addr != hash.Address(a2) {
		t.Fatalf("指针未推进: %+v", refs[0])
	}
	all, err := s.BranchListAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range all {
		if r.Project == "p1" && r.Name == "main" && r.Description == "工作线" {
			found = true
		}
	}
	if !found {
		t.Fatalf("BranchListAll 应含描述: %+v", all)
	}
	if err := s.BranchDescribe(ctx, "p1", "main", "改:主线"); err != nil {
		t.Fatal(err)
	}
	refs, _ = s.BranchList(ctx, "p1")
	if refs[0].Description != "改:主线" {
		t.Fatalf("BranchDescribe 未生效: %+v", refs[0])
	}
	if err := s.BranchDescribe(ctx, "p1", "nope", "x"); !errors.Is(err, ErrBranchNotFound) {
		t.Fatalf("不存在分支应返回 ErrBranchNotFound,得到 %v", err)
	}
}

// statOf 从 ProjectStats 中取指定项目的摘要(不存在即失败)。
func statOf(t *testing.T, s *PG, name string) ProjectStat {
	t.Helper()
	stats, err := s.ProjectStats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range stats {
		if st.Project == name {
			return st
		}
	}
	t.Fatalf("项目 %q 不在 Stats 中", name)
	return ProjectStat{}
}
