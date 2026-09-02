package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/imeepos/cas-kb/internal/repo"
	"github.com/imeepos/cas-kb/internal/view"
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
	if f.has("--json") {
		// 行契约复用 internal/view(与 GET /api/v1/note 同构,TestServeWriteCLIParity 钉死)
		return printJSON(view.NoteRowOf(ref))
	}
	fmt.Printf("path:  %s\naddr:  %s\ntitle: %s\ntags:  %s\n",
		ref.Path, ref.Addr, ref.Note.Meta.Title, strings.Join(ref.Note.Meta.Tags, ","))
	fmt.Printf("body:\n%s\n", string(ref.Body))
	return nil
}

func noteRm(ctx context.Context, r *repo.Repo, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("note rm: 缺少 slug")
	}
	f, err := parseFlags(args, validRmFlags)
	if err != nil {
		return err
	}
	if len(f.pos) < 1 {
		return fmt.Errorf("note rm: 缺少 slug")
	}
	msg := f.get("-m", f.get("--msg", "note rm"))
	if f.has("--stage") {
		if _, err := r.StageRemoveNote(ctx, f.pos[0], msg); err != nil {
			return err
		}
		fmt.Printf("staged rm %s\n", f.pos[0])
		return nil
	}
	snapAddr, err := r.RemoveNote(ctx, f.pos[0], msg)
	if err != nil {
		return err
	}
	fmt.Printf("removed %s\nsnapshot %s\n", f.pos[0], snapAddr)
	return nil
}

func noteLs(ctx context.Context, r *repo.Repo, args []string) error {
	f, err := parseFlags(args, map[string]bool{"--dir": true})
	if err != nil {
		return err
	}
	// 文本模式走轻量读取(不加载正文);--json 需要正文派生摘要
	var refs []*repo.NoteRef
	if f.has("--json") {
		refs, err = r.ListNotes(ctx, f.get("--dir", ""))
	} else {
		refs, err = r.ListNotesMeta(ctx, f.get("--dir", ""))
	}
	if err != nil {
		return err
	}
	if err != nil {
		return err
	}
	if len(refs) == 0 {
		fmt.Println("(no notes)")
		return nil
	}
	if f.has("--json") {
		// 行契约复用 internal/view(摘要派生规则同源)
		return printJSON(view.NoteLsRows(refs))
	}
	for _, ref := range refs {
		fmt.Printf("%s\t%s\n", ref.Path, ref.Note.Meta.Title)
	}
	return nil
}
