package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/imeepos/cas-kb/internal/repo"
)

func noteSet(ctx context.Context, r *repo.Repo, args []string) error {
	f, err := parseFlags(args, validNoteFlags)
	if err != nil {
		return err
	}
	if len(f.pos) < 1 {
		return fmt.Errorf("note set: 缺少 slug")
	}
	in := repo.NoteInput{
		Title: f.get("--title", f.pos[0]),
		Body:  f.get("--body", ""),
	}
	if t := f.get("--tags", ""); t != "" {
		in.Tags = splitTags(t)
	}
	if v := f.get("--time", ""); v != "" {
		if t, err := strconv.ParseInt(v, 10, 64); err == nil {
			in.Time = t
		}
	}
	msg := f.get("-m", f.get("--msg", "note set"))
	if f.has("--stage") {
		_, noteAddr, err := r.StageNote(ctx, f.pos[0], in, msg)
		if err != nil {
			return err
		}
		fmt.Printf("staged %s -> %s\n", f.pos[0], noteAddr)
		return nil
	}
	snapAddr, noteAddr, err := r.SetNote(ctx, f.pos[0], in, msg)
	if err != nil {
		return err
	}
	fmt.Printf("note %s -> %s\nsnapshot %s\n", f.pos[0], noteAddr, snapAddr)
	return nil
}

func splitTags(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
