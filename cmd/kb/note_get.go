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
	f, err := parseFlags(args, nil)
	if err != nil {
		return err
	}
	refs, err := r.ListNotes(ctx)
	if err != nil {
		return err
	}
	if len(refs) == 0 {
		fmt.Println("(no notes)")
		return nil
	}
	if f.has("--json") {
		type row struct {
			Slug      string   `json:"slug"`
			Title     string   `json:"title"`
			Tags      []string `json:"tags"`
			CreatedAt int64    `json:"created_at"`
			Summary   string   `json:"summary"`
		}
		rows := make([]row, 0, len(refs))
		for _, ref := range refs {
			tags := ref.Note.Meta.Tags
			if tags == nil {
				tags = []string{}
			}
			rows = append(rows, row{Slug: ref.Slug, Title: ref.Note.Meta.Title, Tags: tags, CreatedAt: ref.Note.Meta.CreatedAt, Summary: firstSummary(ref.Body)})
		}
		return printJSON(rows)
	}
	for _, ref := range refs {
		fmt.Printf("%s\t%s\n", ref.Slug, ref.Note.Meta.Title)
	}
	return nil
}
