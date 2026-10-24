package diff

import (
	"fmt"
	"strings"
	"testing"
)

func TestCount(t *testing.T) {
	a := []byte("a\nb\nc\n")
	b := []byte("a\nx\nc\nd\n")
	s := Count(a, b)
	if s.Added != 2 || s.Removed != 1 {
		t.Fatalf("stats %+v", s)
	}
	if s := Count(nil, b); s.Added != 4 || s.Removed != 0 {
		t.Fatalf("create %+v", s)
	}
	if s := Count(a, nil); s.Added != 0 || s.Removed != 3 {
		t.Fatalf("delete %+v", s)
	}
	if s := Count(a, a); s.Added != 0 || s.Removed != 0 {
		t.Fatalf("same %+v", s)
	}
}

func TestOpsReconstruct(t *testing.T) {
	a := strings.Split("the quick brown fox jumps over the lazy dog", " ")
	b := strings.Split("the slow brown cat jumps over a lazy dog today", " ")
	ops := Ops(a, b)
	var ra, rb []string
	for _, op := range ops {
		switch op.Kind {
		case ' ':
			ra = append(ra, op.Text)
			rb = append(rb, op.Text)
		case '-':
			ra = append(ra, op.Text)
		case '+':
			rb = append(rb, op.Text)
		}
	}
	if strings.Join(ra, " ") != strings.Join(a, " ") || strings.Join(rb, " ") != strings.Join(b, " ") {
		t.Fatalf("edit script does not reconstruct inputs:\n%v\n%v", ra, rb)
	}
}

func TestUnified(t *testing.T) {
	a := []byte("1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n")
	b := []byte("1\n2\n3\n4\nfive\n6\n7\n8\n9\n10\n")
	out := Unified("a", "b", a, b, 3)
	want := "--- a\n+++ b\n@@ -2,7 +2,7 @@\n 2\n 3\n 4\n-5\n+five\n 6\n 7\n 8\n"
	if out != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out, want)
	}
	if Unified("a", "b", a, a, 3) != "" {
		t.Fatal("identical content must produce no diff")
	}
}

func TestHunksSeparate(t *testing.T) {
	var sb strings.Builder
	for i := 1; i <= 30; i++ {
		fmt.Fprintf(&sb, "line %d\n", i)
	}
	a := []byte(sb.String())
	lines := strings.Split(strings.TrimSuffix(sb.String(), "\n"), "\n")
	lines[2] = "changed"
	lines[25] = "changed"
	b := []byte(strings.Join(lines, "\n") + "\n")
	h := Hunks(a, b, 3)
	if len(h) != 2 {
		t.Fatalf("expected 2 hunks, got %d", len(h))
	}
	if h[0].NewStart != 1 || h[1].OldStart != 23 {
		t.Fatalf("hunk starts %d %d", h[0].NewStart, h[1].OldStart)
	}
}

func TestNoTrailingNewline(t *testing.T) {
	out := Unified("a", "b", []byte("x\ny"), []byte("x\ny\n"), 3)
	if !strings.Contains(out, "No newline at end of file") {
		t.Fatalf("missing marker:\n%s", out)
	}
}

func TestNumbered(t *testing.T) {
	a := []byte("1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n")
	b := []byte("1\n2\nthree\n4\n5\n6\n7\n8\n9\n10\neleven\n")
	full := Numbered(a, b, -1)
	if len(full) != 12 {
		t.Fatalf("whole file should have 12 rows, got %d", len(full))
	}
	starts := 0
	for _, l := range full {
		if l.HunkStart {
			starts++
		}
	}
	if starts != 2 {
		t.Fatalf("expected 2 change runs, got %d", starts)
	}
	if full[2].Kind != '-' || full[2].Old != 3 || full[3].Kind != '+' || full[3].New != 3 {
		t.Fatalf("rows %+v %+v", full[2], full[3])
	}
	ctx := Numbered(a, b, 1)
	seps := 0
	for _, l := range ctx {
		if l.Kind == '@' {
			seps++
		}
	}
	if seps != 2 || ctx[0].Text != "@@ -2,3 +2,3 @@" {
		t.Fatalf("context view %+v", ctx)
	}
	if got := Numbered(nil, []byte("x\n"), 3); len(got) != 2 || got[1].New != 1 {
		t.Fatalf("new file %+v", got)
	}
}
