package tap

import "testing"

func TestCleanPath(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/Users/stef/foo", "/Users/stef/foo"},
		{"//Users/stef/foo", "/Users/stef/foo"},
		{"/Users/stef/Library/Application Support/foo", "/Users/stef/Library/Application Support/foo"},
		{"/../../System/Volumes/Preboot/OS", "/System/Volumes/Preboot/OS"},
		{"//Users/stef/Library/Containers/com.app/Data/x/", "/Users/stef/Library/Containers/com.app/Data/x"},
		{"", ""},
	}
	for _, c := range cases {
		got := cleanPath(c.in)
		if got != c.want {
			t.Errorf("cleanPath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
