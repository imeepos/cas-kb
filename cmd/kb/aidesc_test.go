package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// M3.7:项目描述闭环——create --desc、ls --json、desc 更新/读取、长度纪律。
func TestM37ProjectDescAndJSON(t *testing.T) {
	ctx := context.Background()
	initRepo(t)
	if err := cmdProject(ctx, []string{"create", "alpha", "--desc", "存 alpha 项目知识"}); err != nil {
		t.Fatal(err)
	}
	out, err := captureStdout(t, func() error { return cmdProject(ctx, []string{"ls", "--json"}) })
	if err != nil {
		t.Fatal(err)
	}
	var rows []map[string]interface{}
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("project ls --json 应为合法 JSON: %v\n%s", err, out)
	}
	if !strings.Contains(out, `"name": "alpha"`) || !strings.Contains(out, `"description": "存 alpha 项目知识"`) {
		t.Fatalf("ls --json 应含 name/description: %s", out)
	}
	if err := cmdProject(ctx, []string{"desc", "alpha", "改为新用途"}); err != nil {
		t.Fatal(err)
	}
	out, err = captureStdout(t, func() error { return cmdProject(ctx, []string{"desc", "alpha"}) })
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) != "改为新用途" {
		t.Fatalf("desc 读取不符: %q", out)
	}
	if err := cmdProject(ctx, []string{"create", "big", "--desc", strings.Repeat("长", 513)}); err == nil {
		t.Fatal("超长描述应被拒绝")
	}
}

// M3.7:分支清单与描述——ls --json、desc 更新/读取、提交推进不清空描述。
func TestM37BranchLsAndDesc(t *testing.T) {
	ctx := context.Background()
	initRepo(t)
	setNote(t, "a", "标题A")
	out, err := captureStdout(t, func() error { return cmdBranch(ctx, []string{"ls", "--json"}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"name": "main"`) {
		t.Fatalf("branch ls --json 应含 main: %s", out)
	}
	if err := cmdBranch(ctx, []string{"desc", "main", "工作线"}); err != nil {
		t.Fatal(err)
	}
	setNote(t, "b", "标题B") // 推进分支
	out, err = captureStdout(t, func() error { return cmdBranch(ctx, []string{"ls", "--json"}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"description": "工作线"`) {
		t.Fatalf("推进不应清空描述: %s", out)
	}
	out, err = captureStdout(t, func() error { return cmdBranch(ctx, []string{"desc", "main"}) })
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) != "工作线" {
		t.Fatalf("branch desc 读取不符: %q", out)
	}
}

// M3.7:note ls --json 含派生摘要(展示层,不改对象)。
func TestM37NoteLsJSONSummary(t *testing.T) {
	ctx := context.Background()
	initRepo(t)
	if err := cmdNote(ctx, []string{"set", "s1", "--title", "T1", "--body", "首段摘要行\n第二行", "-m", "add"}); err != nil {
		t.Fatal(err)
	}
	out, err := captureStdout(t, func() error { return cmdNote(ctx, []string{"ls", "--json"}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"slug": "s1"`) || !strings.Contains(out, `"summary": "首段摘要行"`) {
		t.Fatalf("note ls --json 应含 slug/summary: %s", out)
	}
	var rows []map[string]interface{}
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("应为合法 JSON: %v", err)
	}
}

// M3.7 回补:--desc 旗标与 desc 文本路径统一 TrimSpace。
func TestM37DescTrimUnify(t *testing.T) {
	ctx := context.Background()
	initRepo(t)
	if err := cmdProject(ctx, []string{"create", "tt", "--desc", "  环绕空格  "}); err != nil {
		t.Fatal(err)
	}
	out, err := captureStdout(t, func() error { return cmdProject(ctx, []string{"desc", "tt"}) })
	if err != nil {
		t.Fatal(err)
	}
	if out != "环绕空格\n" {
		t.Fatalf("desc 应存储去空格后的值,得到 %q", out)
	}
}
