package lintdirectives

import (
	"reflect"
	"testing"
)

func TestCheck(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []Finding
	}{
		{
			name: "a trailing errcheck exception with a reason is accepted",
			src: `package p

func f() {
	g() //nolint:errcheck // best-effort cleanup
}
`,
		},
		{
			name: "a staticcheck line ignore is rejected",
			src: `package p

//lint:ignore U1000 kept for later
func dead() {}
`,
			want: []Finding{{Line: 3, Rule: RuleStaticcheckDirective}},
		},
		{
			name: "a staticcheck file ignore is rejected",
			src: `//lint:file-ignore U1000 generated
package p
`,
			want: []Finding{{Line: 1, Rule: RuleStaticcheckDirective}},
		},
		{
			name: "a nolint above the package clause is rejected as file-wide",
			src: `//nolint:errcheck // whole file
package p
`,
			want: []Finding{{Line: 1, Rule: RuleStandaloneNolint}},
		},
		{
			name: "a nolint on its own line above a declaration is rejected",
			src: `package p

//nolint:errcheck // whole function
func f() {
	g()
}
`,
			want: []Finding{{Line: 3, Rule: RuleStandaloneNolint}},
		},
		{
			name: "a nolint on its own line inside a body is rejected",
			src: `package p

func f() {
	//nolint:errcheck // next statement
	g()
}
`,
			want: []Finding{{Line: 4, Rule: RuleStandaloneNolint}},
		},
		{
			name: "a trailing nolint for another linter is rejected",
			src: `package p

func f() {
	g() //nolint:gosec // reviewed
}
`,
			want: []Finding{{Line: 4, Rule: RuleForeignNolint}},
		},
		{
			name: "a trailing nolint that also names another linter is rejected",
			src: `package p

func f() {
	g() //nolint:errcheck,unused // reviewed
}
`,
			want: []Finding{{Line: 4, Rule: RuleForeignNolint}},
		},
		{
			name: "a trailing nolint naming no linter is rejected",
			src: `package p

func f() {
	g() //nolint // everything
}
`,
			want: []Finding{{Line: 4, Rule: RuleForeignNolint}},
		},
		{
			name: "a spaced nolint is still a directive",
			src: `package p

// nolint:errcheck // spaced
func f() {}
`,
			want: []Finding{{Line: 3, Rule: RuleStandaloneNolint}},
		},
		{
			name: "directive text inside a string literal is not a directive",
			src:  "package p\n\nconst s = `\n//nolint:errcheck\n//lint:ignore U1000 x\n`\n",
		},
		{
			name: "prose that mentions nolint is not a directive",
			src: `package p

// A nolintable thing is fine; so is talk of //lint: in the middle.
func f() {}
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Check("p.go", []byte(tt.src))
			if err != nil {
				t.Fatalf("Check: %v", err)
			}
			for i := range tt.want {
				tt.want[i].Path = "p.go"
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Check = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestCheckRejectsAnUnscannableFile(t *testing.T) {
	if _, err := Check("p.go", []byte("package p\n\nconst s = \"unterminated\n")); err == nil {
		t.Fatal("Check succeeded on a file with an unterminated string literal")
	}
}
