package main

import (
	"context"
	"fmt"
	"time"

	"github.com/imeepos/cas-kb/internal/hash"
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

// shortAddr 截断地址便于展示。
func shortAddr(a hash.Address) string {
	if len(a) > 16 {
		return string(a[:16])
	}
	return string(a)
}
