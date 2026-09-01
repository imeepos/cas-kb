package main

import (
	"context"
	"fmt"
)

func cmdDiff(ctx context.Context, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("diff: 需要 base 与 tip 两个参数")
	}
	r, s, err := openRepo(ctx)
	if err != nil {
		return err
	}
	defer s.Close()
	changes, err := r.Diff(ctx, args[0], args[1])
	if err != nil {
		return err
	}
	if len(changes) == 0 {
		fmt.Println("(no changes)")
		return nil
	}
	for _, c := range changes {
		switch c.Type {
		case "added":
			fmt.Printf("A  %s -> %s\n", c.Slug, shortAddr(c.To))
		case "removed":
			fmt.Printf("D  %s <- %s\n", c.Slug, shortAddr(c.From))
		case "updated":
			fmt.Printf("M  %s %s -> %s\n", c.Slug, shortAddr(c.From), shortAddr(c.To))
		}
	}
	return nil
}
