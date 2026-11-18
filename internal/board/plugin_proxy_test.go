package board

import (
	"context"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/db"
)

func TestPluginForwardOwnershipValidationAndCleanup(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	generic := addForward(t, b, nil)
	req := api.CreateProxyForward{Port: 3456, Name: "tool"}
	_, err := b.CreatePluginForward(ctx, User, "p", "", req)
	wantCode(t, err, api.CodeForbidden)
	bad := req
	bad.StartPath = "/../outside"
	_, err = b.CreatePluginForward(ctx, System, "p", "", bad)
	wantCode(t, err, api.CodeValidation)
	f, err := b.CreatePluginForward(ctx, System, "p", "stable-run", req)
	if err != nil {
		t.Fatal(err)
	}
	if f.Plugin != "p" || f.TaskID != "stable-run" {
		t.Fatalf("ownership missing: %+v", f)
	}
	q, err := b.PluginForwards(ctx, System, "q")
	if err != nil || len(q) != 0 {
		t.Fatalf("other namespace: %+v %v", q, err)
	}
	err = b.DeletePluginForward(ctx, System, "q", f.ID)
	wantCode(t, err, api.CodeNotFound)
	err = b.DeletePluginForward(ctx, System, "p", generic.ID)
	wantCode(t, err, api.CodeNotFound)
	if err := b.ClearPluginForwards(ctx, System, "p", "wrong-run"); err != nil {
		t.Fatal(err)
	}
	if own, _ := b.PluginForwards(ctx, System, "p"); len(own) != 1 {
		t.Fatal("wrong run removed forward")
	}
	if err := b.ClearPluginForwards(ctx, System, "p", "stable-run"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.LiveProxyForward(ctx, System, f.ID); err == nil {
		t.Fatal("task cleanup left forward")
	}
	_, err = b.CreatePluginForward(ctx, System, "q", "", req)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.ClearPluginForwards(ctx, System, "", ""); err != nil {
		t.Fatal(err)
	}
	all, err := b.ProxyForwards(ctx, User, nil)
	if err != nil || len(all) != 1 || all[0].ID != generic.ID {
		t.Fatalf("stale cleanup: %+v %v", all, err)
	}
	evs, err := db.EventsSince(b.db, 0, 100, true)
	if err != nil {
		t.Fatal(err)
	}
	removed := 0
	for _, e := range evs {
		if e.Kind == api.KindProxyForwardRemoved {
			removed++
		}
	}
	if removed != 2 {
		t.Fatalf("removal events=%d", removed)
	}
}
