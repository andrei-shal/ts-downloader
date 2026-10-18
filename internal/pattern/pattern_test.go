package pattern

import "testing"

func TestBuildURL(t *testing.T) {
	cases := []struct {
		base, tmpl string
		n, pad     int
		want       string
	}{
		{"https://h/p/", "seg_{}.ts", 7, 0, "https://h/p/seg_7.ts"},
		{"https://h/p", "seg_{}.ts", 7, 0, "https://h/p/seg_7.ts"},
		{"https://h/p//", "/seg_{}.ts", 42, 5, "https://h/p/seg_00042.ts"},
		{"https://h/p/", "a{}b.ts?x=1", 123, 2, "https://h/p/a123b.ts?x=1"},
		{"", "https://h/{}.ts", 1, 0, "https://h/1.ts"},
	}
	for _, c := range cases {
		got, err := BuildURL(c.base, c.tmpl, c.n, c.pad)
		if err != nil || got != c.want {
			t.Errorf("BuildURL(%q,%q,%d,%d) = %q, %v; want %q", c.base, c.tmpl, c.n, c.pad, got, err, c.want)
		}
	}
}

func TestValidate(t *testing.T) {
	if Validate("seg.ts") == nil {
		t.Error("expected error without {}")
	}
	if Validate("{}_{}.ts") == nil {
		t.Error("expected error with two {}")
	}
	if Validate("seg{}.ts") != nil {
		t.Error("unexpected error")
	}
}
