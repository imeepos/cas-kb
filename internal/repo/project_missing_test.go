package repo

import (
	"context"
	"strings"
	"testing"
)

// 复盘 P1:向未创建的项目写入,报错应给出可行动提示(先 project create),
// 而不是裸的外键约束错误。
func TestSetNoteProjectMissing(t *testing.T) {
	ctx := context.Background()
	s, _ := freshStore(t)
	r := Open(s, Config{Project: "ghost", Branch: "main"})
	_, _, err := r.SetNote(ctx, "a", NoteInput{Title: "A", Body: "b"}, "add a")
	if err == nil || !strings.Contains(err.Error(), "project create") {
		t.Fatalf("应报「先 project create」提示: %v", err)
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("提示应含项目名: %v", err)
	}
}
