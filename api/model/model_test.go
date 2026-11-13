package model

import "testing"

func TestParseLocation(t *testing.T) {
	cases := []struct {
		in   string
		want Location
	}{
		{"/src/app", Location{Dir: "/src/app"}},
		{"gpu1:~/proj", Location{Host: "gpu1", Dir: "~/proj"}},
		{"gpu1:", Location{Host: "gpu1", Dir: "~"}},
		{"./a:b", Location{Dir: "./a:b"}},
	}
	for _, c := range cases {
		got, err := ParseLocation(c.in)
		if err != nil || got != c.want {
			t.Errorf("ParseLocation(%q) = %+v, %v; want %+v", c.in, got, err, c.want)
		}
	}
	if _, err := ParseLocation(" "); err == nil {
		t.Error("empty location should fail")
	}
}

func TestValidName(t *testing.T) {
	for _, ok := range []string{"api-tests", "a", "x1"} {
		if ValidName(ok) != nil {
			t.Errorf("%q should be valid", ok)
		}
	}
	for _, bad := range []string{"", "-x", "Api", "a b", "a/b"} {
		if ValidName(bad) == nil {
			t.Errorf("%q should be invalid", bad)
		}
	}
}
