package main

import (
	"testing"
)

// TestServeSortIDResolution pins the --sort-id flag/env/default resolution on ServeCmd.
func TestServeSortIDResolution(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		cmd, err := parseServe(nil)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if cmd.SortID != 2 {
			t.Fatalf("SortID = %d, want 2 (default)", cmd.SortID)
		}
	})

	t.Run("flag-override", func(t *testing.T) {
		cmd, err := parseServe([]string{"--sort-id=31"})
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if cmd.SortID != 31 {
			t.Fatalf("SortID = %d, want 31 (flag)", cmd.SortID)
		}
	})

	t.Run("env-override", func(t *testing.T) {
		t.Setenv("TAKUHAI_DMHY_SORT_ID", "7")
		cmd, err := parseServe(nil)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if cmd.SortID != 7 {
			t.Fatalf("SortID = %d, want 7 (env)", cmd.SortID)
		}
	})

	t.Run("empty-env-fails-fast", func(t *testing.T) {
		t.Setenv("TAKUHAI_DMHY_SORT_ID", "")
		if _, err := parseServe(nil); err == nil {
			t.Fatal("parse: want error on explicitly-empty TAKUHAI_DMHY_SORT_ID, got nil")
		}
	})
}
