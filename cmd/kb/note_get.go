package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/imeepos/cas-kb/internal/repo"
)

func noteGet(ctx context.Context, r *repo.Repo, args []string) error {
	f, err := parseFlags(args, map[string]bool{"--at": true})
	if err != nil {
		return err
	}
	if len(f.pos) < 1 {
		return fmt.Errorf("note get: 缺少 slug")
	}
	var ref *repo.NoteRef
	if at := f.get("--at", ""); at != "" {
		ref, err = r.NoteAt(ctx, f.pos[0], at)
	} else {
		ref, err = r.Note(ctx, f.pos[0])
	}
	if err != nil {
		return err
	}
	fmt.Printf("slug:  %s\naddr:  %s\ntitle: %s\ntags:  %s\n",
		ref.Slug, ref.Addr, ref.Note.Meta.Title, strings.Join(ref.Note.Meta.Tags, ","))
	fmt.Printf("body:\n%s\n", string(ref.Body))
	return nil
}

func noteRm(ctx context.Context, r *repo.Repo, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("note rm: 缺少 slug")
	}
	f, err := parseFlags(args, map[string]bool{"-m": true, "--msg": true})
	if err != nil {
		return err
	}
	if len(f.pos) < 1 {
		return fmt.Errorf("note rm: 缺少 slug")
	}
	msg := f.get("-m", f.get("--msg", "note rm"))
	snapAddr, err := r.RemoveNote(ctx, f.pos[0], msg)
	if err != nil {
		return err
	}
	fmt.Printf("removed %s\nsnapshot %s\n", f.pos[0], snapAddr)
	return nil
}

func noteLs(ctx context.Context, r *repo.Repo, args []string) error {
	refs, err := r.ListNotes(ctx)
	if err != nil {
		return err
	}
	if len(refs) == 0 {
		fmt.Println("(no notes)")
		return nil
	}
	for _, ref := range refs {
		fmt.Printf("%s\t%s\n", ref.Slug, ref.Note.Meta.Title)
	}
	return nil
}
