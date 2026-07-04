package main

import "testing"

func TestServeDefaultsAndOverrides(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		cmd, err := parseServe(nil)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if cmd.BaseURL != "https://nyaa.si" || cmd.Category != "1_0" || cmd.Filter != "0" {
			t.Fatalf("defaults = %+v", cmd)
		}
	})

	t.Run("flag-override", func(t *testing.T) {
		cmd, err := parseServe([]string{"--query=frieren", "--category=1_2", "--filter=2"})
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if cmd.Query != "frieren" || cmd.Category != "1_2" || cmd.Filter != "2" {
			t.Fatalf("flags = %+v", cmd)
		}
	})

	t.Run("env-override", func(t *testing.T) {
		t.Setenv("TAKUHAI_NYAA_CATEGORY", "1_4")
		cmd, err := parseServe(nil)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if cmd.Category != "1_4" {
			t.Fatalf("Category = %q, want 1_4", cmd.Category)
		}
	})
}
