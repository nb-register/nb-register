package resultdata

import "testing"

func TestBuilder(t *testing.T) {
	got := New().Add("a", "b").Add("skip", nil).Merge(map[string]any{"c": 3}).Map()
	if got["a"] != "b" || got["c"] != 3 {
		t.Fatalf("unexpected builder map: %#v", got)
	}
}

func TestStructRoundTrip(t *testing.T) {
	data := map[string]any{"ok": true, "name": "nb"}
	st := Struct(data)
	got := Map(st)
	if got["ok"] != true || got["name"] != "nb" {
		t.Fatalf("unexpected struct map: %#v", got)
	}
}
