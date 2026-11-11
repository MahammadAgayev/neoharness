package model

import "testing"

func TestParseLocationDrivePath(t *testing.T) {
	cases := []struct {
		in   string
		want Location
	}{
		{`C:\src\app`, Location{Dir: `C:\src\app`}},
		{"c:/src/app", Location{Dir: "c:/src/app"}},
		{"D:", Location{Dir: "D:"}},
		{"gpu1:~/proj", Location{Host: "gpu1", Dir: "~/proj"}},
		{"c:proj", Location{Host: "c", Dir: "proj"}},
	}
	for _, c := range cases {
		got, err := ParseLocation(c.in)
		if err != nil || got != c.want {
			t.Errorf("ParseLocation(%q) = %+v, %v; want %+v", c.in, got, err, c.want)
		}
	}
}
