package fsx

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBoundedWalkCountsIgnoredEntriesAndSharesBudget(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 300; i++ {
		if e := os.WriteFile(filepath.Join(dir, fmt.Sprint(i)), nil, 0600); e != nil {
			t.Fatal(e)
		}
	}
	root, e := os.OpenRoot(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer root.Close()
	budget := 3
	visited := 0
	visit := func(name string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if name != "." {
			visited++
		}
		return nil
	}
	if e := BoundedWalk(context.Background(), root, ".", &budget, visit); !errors.Is(e, ErrWalkLimit) {
		t.Fatalf("bound ignored: %v", e)
	}
	if visited != 3 || budget != 0 {
		t.Fatalf("visited=%d remaining=%d", visited, budget)
	}
	if e := BoundedWalk(context.Background(), root, ".", &budget, visit); !errors.Is(e, ErrWalkLimit) || visited != 3 {
		t.Fatal("shared budget reset")
	}
}

func TestBoundedWalkCancelsDuringChunkAndRejectsDepth(t *testing.T) {
	for _, mode := range []string{"cancel", "depth"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			name := "file"
			if mode == "depth" {
				name = strings.Repeat("dir/", 34) + name
			}
			if e := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0700); e != nil {
				t.Fatal(e)
			}
			if e := os.WriteFile(filepath.Join(dir, name), nil, 0600); e != nil {
				t.Fatal(e)
			}
			if mode == "cancel" {
				for i := 0; i < 300; i++ {
					if e := os.WriteFile(filepath.Join(dir, fmt.Sprint(i)), nil, 0600); e != nil {
						t.Fatal(e)
					}
				}
			}
			root, e := os.OpenRoot(dir)
			if e != nil {
				t.Fatal(e)
			}
			defer root.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			budget := 1000
			e = BoundedWalk(ctx, root, ".", &budget, func(name string, d fs.DirEntry, e error) error {
				if mode == "cancel" && name != "." {
					cancel()
				}
				return e
			})
			want := ErrWalkLimit
			if mode == "cancel" {
				want = context.Canceled
			}
			if !errors.Is(e, want) {
				t.Fatalf("got %v want %v", e, want)
			}
			if mode == "cancel" && budget != 999 {
				t.Fatalf("walk continued after cancellation: remaining=%d", budget)
			}
		})
	}
}
