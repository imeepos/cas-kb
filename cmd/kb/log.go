package main

import (
	"context"
	"fmt"
	"time"

	"github.com/imeepos/cas-kb/internal/hash"
	"github.com/imeepos/cas-kb/internal/view"
)

func cmdLog(ctx context.Context, args []string) error {
	r, s, err := openRepo(ctx)
	if err != nil {
		return err
	}
	defer s.Close()
	entries, err := r.Log(ctx)
	if err != nil {
		return err
	}
	for _, e := range entries {
		ts := time.Unix(e.Snapshot.Time, 0).Format("2006-01-02 15:04:05")
		parent := "none"
		if len(e.Snapshot.Parents) > 0 {
			parent = shortAddr(e.Snapshot.Parents[0])
			// 合并快照(M5)含两个 parents:追加第二亲短标识,
			// 与既有行格式兼容(first-parent 链不变)
			if len(e.Snapshot.Parents) > 1 {
				parent += "," + shortAddr(e.Snapshot.Parents[1])
			}
		}
		msg := e.Snapshot.Message
		if msg == "" {
			msg = "(no message)"
		}
		fmt.Printf("%s  %s  parent=%s  %s\n", shortAddr(e.Addr), ts, parent, msg)
	}
	if len(entries) == 0 {
		fmt.Println("(no commits)")
	}
	return nil
}

// shortAddr 截断地址便于展示;委托 view.ShortAddr(与 /api/v1/log 的短标识同构)。
func shortAddr(a hash.Address) string { return view.ShortAddr(a) }
