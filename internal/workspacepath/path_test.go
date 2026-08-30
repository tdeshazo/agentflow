package workspacepath

import "testing"

func TestClean(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
		ok    bool
	}{
		{name: "glob", value: "src/**", want: "src/**", ok: true},
		{name: "safe parent segment", value: "src/../tests/**", want: "tests/**", ok: true},
		{name: "portable separators", value: `.\docs\**`, want: "docs/**", ok: true},
		{name: "forward slash traversal", value: "src/../../outside", ok: false},
		{name: "backslash traversal", value: `src\..\..\outside`, ok: false},
		{name: "absolute", value: "/outside", ok: false},
		{name: "Windows drive absolute", value: `C:\outside`, ok: false},
		{name: "Windows drive relative", value: `C:outside`, ok: false},
		{name: "UNC", value: `\\server\share`, ok: false},
		{name: "NUL", value: "src/\x00outside", ok: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := Clean(test.value)
			if got != test.want || ok != test.ok {
				t.Fatalf("Clean(%q) = (%q, %t), want (%q, %t)", test.value, got, ok, test.want, test.ok)
			}
		})
	}
}
