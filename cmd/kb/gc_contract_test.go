package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/imeepos/cas-kb/internal/hash"
	"github.com/imeepos/cas-kb/internal/store"
)

func TestExportBranchesFileWritesStableContract(t *testing.T) {
	t.Chdir(t.TempDir())
	branches := []store.BranchRef{
		{Project: "default", Name: "main", Addr: hash.Address("sha256:" + strings.Repeat("a", 64))},
		{Project: "lab", Name: "dev", Addr: hash.Address("sha256:" + strings.Repeat("b", 64))},
	}
	if err := exportBranchesFile(context.Background(), branches); err != nil {
		t.Fatal(err)
	}
	entries, err := filepath.Glob("branches-backup-*.json")
	if err != nil || len(entries) != 1 {
		t.Fatalf("应恰好生成一个备份文件,got %v (err=%v)", entries, err)
	}
	payload, err := os.ReadFile(entries[0])
	if err != nil {
		t.Fatal(err)
	}
	var rows []map[string]string
	if err := json.Unmarshal(payload, &rows); err != nil {
		t.Fatalf("备份应为合法 JSON: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("应有两行分支,got %d", len(rows))
	}
	if rows[0]["name"] != "main" || rows[0]["addr"] != string(branches[0].Addr) {
		t.Fatalf("小写键 name/addr 契约不符: %v", rows[0])
	}
	if rows[0]["project"] != "default" {
		t.Fatalf("小写键 project 契约不符: %v", rows[0])
	}
	if _, ok := rows[0]["Project"]; ok {
		t.Fatal("备份不应出现大写键 Project")
	}
	if _, ok := rows[0]["Name"]; ok {
		t.Fatal("备份不应出现大写键 Name")
	}
}
