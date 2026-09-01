package store

import (
	"context"
	"errors"
	"testing"

	"github.com/imeepos/cas-kb/internal/object"
)

// ProjectGet 读取单个项目(名称/描述/分支数);不存在返回 ErrProjectNotFound。
func TestProjectGet(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	if err := s.ProjectCreate(ctx, "pgt", "项目描述"); err != nil {
		t.Fatal(err)
	}
	a1, err := s.Put(ctx, object.KindBlob, []byte("v1"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.BranchSet(ctx, "pgt", "main", a1); err != nil {
		t.Fatal(err)
	}
	a2, err := s.Put(ctx, object.KindBlob, []byte("v2"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.BranchSet(ctx, "pgt", "dev", a2); err != nil {
		t.Fatal(err)
	}
	st, err := s.ProjectGet(ctx, "pgt")
	if err != nil {
		t.Fatal(err)
	}
	if st.Project != "pgt" || st.Description != "项目描述" || st.Branches != 2 {
		t.Fatalf("ProjectGet 不符: %+v", st)
	}
	if _, err := s.ProjectGet(ctx, "ghost"); !errors.Is(err, ErrProjectNotFound) {
		t.Fatalf("不存在项目应 ErrProjectNotFound,得到 %v", err)
	}
}
