package lintdirectives

import (
	"reflect"
	"testing"
)

func TestCheck(t *testing.T) {
	tests := []struct {
		name  string
		src   string
		lines []int
	}{
		{
			name: "a file without directives passes",
			src: `package p

// f has an ordinary doc comment.
func f() {
	g()
}
`,
		},
		{
			name:  "a trailing nolint is rejected",
			src:   "package p\n\nfunc f() {\n\tg() //nolint:errcheck // best-effort cleanup\n}\n",
			lines: []int{4},
		},
		{
			name:  "a standalone nolint is rejected",
			src:   "package p\n\n//nolint:errcheck // whole function\nfunc f() {}\n",
			lines: []int{3},
		},
		{
			name:  "a nolint above the package clause is rejected",
			src:   "//nolint // whole file\npackage p\n",
			lines: []int{1},
		},
		{
			name:  "a nolint with extra slashes or spaces is still a directive",
			src:   "package p\n\n// /nolint:errcheck\nfunc f() {\n\tg() ///nolint:gosec\n}\n",
			lines: []int{3, 5},
		},
		{
			name:  "a staticcheck line ignore is rejected",
			src:   "package p\n\n//lint:ignore U1000 kept for later\nfunc dead() {}\n",
			lines: []int{3},
		},
		{
			name:  "a staticcheck file ignore is rejected",
			src:   "//lint:file-ignore U1000 generated\npackage p\n",
			lines: []int{1},
		},
		{
			name: "directive text inside a string literal is not a directive",
			src:  "package p\n\nconst s = `\n//nolint:errcheck\n//lint:ignore U1000 x\n`\n",
		},
		{
			name: "prose that mentions nolint is not a directive",
			src:  "package p\n\n// A nolintable thing is fine; so is talk of //lint: in the middle.\nfunc f() {}\n",
		},
		{
			name: "a tab after the slashes is not a directive",
			src:  "package p\n\n//\tnolint:errcheck is prose here\nfunc f() {}\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Check("p.go", []byte(tt.src))
			if err != nil {
				t.Fatalf("Check: %v", err)
			}
			var want []Finding
			for _, line := range tt.lines {
				want = append(want, Finding{Path: "p.go", Line: line})
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("Check = %+v, want %+v", got, want)
			}
		})
	}
}

func TestCheckRejectsAnUnscannableFile(t *testing.T) {
	if _, err := Check("p.go", []byte("package p\n\nconst s = \"unterminated\n")); err == nil {
		t.Fatal("Check succeeded on a file with an unterminated string literal")
	}
}
