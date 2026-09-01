package main

import (
	"context"
	"fmt"
)

func cmdGC(ctx context.Context, args []string) error {
	r, s, err := openRepo(ctx)
	if err != nil {
		return err
	}
	defer s.Close()
	res, err := r.GC(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("GC: 标记 %d,清扫 %d\n", res.Marked, res.Swept)
	return nil
}
