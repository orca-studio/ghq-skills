package skills

import "testing"

func TestLockFindRemoveUses(t *testing.T) {
	l := &Lock{Skill: []Skill{
		{Name: "lark-base", Repo: "github.com/larksuite/cli", Link: "lark-base"},
		{Name: "lark-doc", Repo: "github.com/larksuite/cli", Link: "lark-doc"},
		{Name: "grilling", Repo: "github.com/mattpocock/skills", Link: "grilling"},
	}}

	if _, ok := l.Find("lark-base"); !ok {
		t.Fatal("Find(lark-base): want present")
	}
	if _, ok := l.Find("nope"); ok {
		t.Fatal("Find(nope): want absent")
	}
	if n := l.Uses("github.com/larksuite/cli"); n != 2 {
		t.Fatalf("Uses(larksuite/cli) = %d, want 2", n)
	}

	if !l.Remove("lark-base") {
		t.Fatal("Remove(lark-base): want true")
	}
	if l.Remove("lark-base") {
		t.Fatal("Remove(lark-base) twice: want false")
	}
	if _, ok := l.Find("lark-base"); ok {
		t.Fatal("lark-base still found after Remove")
	}
	if n := l.Uses("github.com/larksuite/cli"); n != 1 {
		t.Fatalf("Uses(larksuite/cli) after remove = %d, want 1", n)
	}
	if len(l.Skill) != 2 {
		t.Fatalf("len after remove = %d, want 2", len(l.Skill))
	}
}
