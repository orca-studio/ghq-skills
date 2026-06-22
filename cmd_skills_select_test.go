package main

import (
	"testing"

	"github.com/x-motemen/ghq/skills"
)

func selNames(found []skills.Found) string {
	s := ""
	for i, f := range found {
		if i > 0 {
			s += ","
		}
		s += f.Name
	}
	return s
}

func TestParseSelection(t *testing.T) {
	found := []skills.Found{
		{Name: "tdd"}, {Name: "teach"}, {Name: "triage"},
	}
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"", "tdd,teach,triage", false},
		{"all", "tdd,teach,triage", false},
		{"a", "tdd,teach,triage", false},
		{"1", "tdd", false},
		{"1 3", "tdd,triage", false},
		{"1,3", "tdd,triage", false},
		{"teach", "teach", false},
		{"3 teach", "triage,teach", false},
		{"2 teach", "teach", false}, // dup collapses
		{"9", "", true},
		{"nope", "", true},
	}
	for _, tt := range tests {
		got, err := parseSelection(tt.in, found)
		if tt.wantErr {
			if err == nil {
				t.Errorf("parseSelection(%q): expected error", tt.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseSelection(%q): %v", tt.in, err)
			continue
		}
		if g := selNames(got); g != tt.want {
			t.Errorf("parseSelection(%q) = %q, want %q", tt.in, g, tt.want)
		}
	}
}
