package handler

import (
	"testing"

	"mempalace/server/internal/storage"
)

// redir builds a redirect map from (fromRoom→toRoom) pairs, all in wing "w".
func redir(pairs ...[2]string) map[string]storage.Redirect {
	m := map[string]storage.Redirect{}
	for _, p := range pairs {
		m[rkey("w", p[0])] = storage.Redirect{
			FromWing: "w", FromRoom: p[0], ToWing: "w", ToRoom: p[1], Reason: p[0] + "→" + p[1],
		}
	}
	return m
}

func TestResolveChain_NoRedirect(t *testing.T) {
	tw, tr, chain, reason, redirected := resolveChain(redir(), "w", "Auth")
	if redirected || tw != "w" || tr != "Auth" {
		t.Fatalf("want (w,Auth,false), got (%s,%s,%v)", tw, tr, redirected)
	}
	if len(chain) != 1 || chain[0] != "w/Auth" {
		t.Errorf("chain = %v, want [w/Auth]", chain)
	}
	if reason != "" {
		t.Errorf("reason = %q, want empty", reason)
	}
}

func TestResolveChain_SingleHop(t *testing.T) {
	tw, tr, chain, reason, redirected := resolveChain(redir([2]string{"Auth", "Authentication"}), "w", "Auth")
	if !redirected || tw != "w" || tr != "Authentication" {
		t.Fatalf("want (w,Authentication,true), got (%s,%s,%v)", tw, tr, redirected)
	}
	if len(chain) != 2 || chain[0] != "w/Auth" || chain[1] != "w/Authentication" {
		t.Errorf("chain = %v", chain)
	}
	if reason != "Auth→Authentication" {
		t.Errorf("reason = %q", reason)
	}
}

func TestResolveChain_MultiHop(t *testing.T) {
	m := redir([2]string{"A", "B"}, [2]string{"B", "C"}, [2]string{"C", "D"})
	tw, tr, chain, reason, redirected := resolveChain(m, "w", "A")
	if !redirected || tr != "D" || tw != "w" {
		t.Fatalf("want terminal D, got %s/%s", tw, tr)
	}
	if len(chain) != 4 {
		t.Errorf("chain = %v, want 4 hops A→B→C→D", chain)
	}
	if reason != "A→B" { // reason of the FIRST hop
		t.Errorf("reason = %q, want first-hop reason", reason)
	}
}

func TestResolveChain_CycleStopsSafely(t *testing.T) {
	// A→B→A: must terminate at B, never loop.
	m := redir([2]string{"A", "B"}, [2]string{"B", "A"})
	tw, tr, chain, _, redirected := resolveChain(m, "w", "A")
	if !redirected || tr != "B" || tw != "w" {
		t.Fatalf("want terminal B, got %s/%s", tw, tr)
	}
	if len(chain) != 2 {
		t.Errorf("chain = %v, want [w/A w/B]", chain)
	}
}

func TestResolveChain_SelfLoop(t *testing.T) {
	// A→A (shouldn't be creatable, but must not spin): treated as no move.
	tw, tr, chain, _, redirected := resolveChain(redir([2]string{"A", "A"}), "w", "A")
	if redirected || tr != "A" || tw != "w" || len(chain) != 1 {
		t.Fatalf("self-loop mishandled: %s/%s redirected=%v chain=%v", tw, tr, redirected, chain)
	}
}

func TestResolveChain_LongChainWithinCap(t *testing.T) {
	// A linear chain longer than a couple hops still fully resolves.
	pairs := [][2]string{}
	names := []string{"r0", "r1", "r2", "r3", "r4", "r5"}
	for i := 0; i < len(names)-1; i++ {
		pairs = append(pairs, [2]string{names[i], names[i+1]})
	}
	_, tr, chain, _, redirected := resolveChain(redir(pairs...), "w", "r0")
	if !redirected || tr != "r5" || len(chain) != 6 {
		t.Fatalf("want terminal r5 (6 hops), got %s chain=%v", tr, chain)
	}
}

func TestRedirectToolsRegistered(t *testing.T) {
	s := newRegistry()
	for _, name := range []string{
		"mempalace_redirect_room", "mempalace_list_redirects",
		"mempalace_resolve_room", "mempalace_delete_redirect",
	} {
		if _, ok := s.tools[name]; !ok {
			t.Errorf("redirect tool not registered: %s", name)
		}
		if _, ok := s.router[name]; !ok {
			t.Errorf("redirect tool %s has no router entry", name)
		}
	}
}

// resolveRoom must be a safe no-op when the redirect store is absent (nil),
// so the registry/nil-dependency server never panics on read/write tools.
func TestResolveRoom_NilStore(t *testing.T) {
	s := newRegistry() // built with nil redirects
	w, r, info, err := s.resolveRoom(reqCtx(), "w", "Auth")
	if err != nil || info != nil || w != "w" || r != "Auth" {
		t.Fatalf("nil store should pass through unchanged: %s/%s info=%v err=%v", w, r, info, err)
	}
}
