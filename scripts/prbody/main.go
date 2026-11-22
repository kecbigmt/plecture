// Command prbody is scripts/check-pr-body.sh's implementation: it parses a
// pull request body as GitHub-flavored Markdown, because a text scan cannot
// tell a closing keyword in prose from one quoted in code, nor an image under
// "## Overview" from one in a later section.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

func main() {
	repo := flag.String("repo", "", "owner/name of the repository the pull request targets")
	flag.Parse()
	if *repo == "" || strings.Count(*repo, "/") != 1 || flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: check-pr-body.sh --repo <owner>/<name> <body-file>")
		os.Exit(2)
	}
	path := flag.Arg(0)
	body, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "check-pr-body:", err)
		os.Exit(2)
	}
	findings := Check(body, *repo)
	for _, f := range findings {
		fmt.Printf("%s:%d: %s: %s\n", path, f.Line, f.Rule, f.Message)
	}
	if len(findings) > 0 {
		os.Exit(1)
	}
	fmt.Println("check-pr-body: passed")
}
