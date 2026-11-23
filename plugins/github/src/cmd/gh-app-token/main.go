// gh-app-token mints and caches a GitHub App installation access token,
// composed by the github plugin's gh_app_guard effect into a `gh` wrapper
// (see plugins/github/scripts/gh-app-guard) — it never talks to `gh` itself,
// only to GitHub's App-authentication endpoints.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/kecbigmt/plecture/plugins/github/src/internal/apptoken"
	"github.com/kecbigmt/plecture/plugins/github/src/internal/githubapp"
	"github.com/kecbigmt/plecture/plugins/github/src/internal/tokencommand"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "print":
		err = cmdPrint(os.Args[2:], os.Stdout)
	case "credential":
		err = cmdCredential(os.Args[2:], os.Stdin, os.Stdout)
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		// Every error reaching here is built from githubapp/apptoken or
		// tokencommand, which never fold key or token bytes into an error
		// string — this prefix is the only thing added at this layer.
		fmt.Fprintf(os.Stderr, "gh-app-token: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  gh-app-token print --app-id <id> (--installation-id <id> | --owner <owner> --repo <repo>) --private-key-path <path> --cache-path <path> [--base-url <url>] [--skew <duration>]
  gh-app-token credential --app-id <id> (--installation-id <id> | --owner <owner> --repo <repo>) --private-key-path <path> --cache-path <path> [--base-url <url>] [--skew <duration>] get
  gh-app-token credential --token-command <json-argv> get`)
}

// cmdPrint mints (or reuses a cached, unexpired) installation token and
// writes exactly the token, newline-terminated, to stdout — nothing else.
// A caller capturing this command's stdout (the gh-app-guard wrapper does,
// via `$(...)`) gets the token and nothing it would need to strip.
func cmdPrint(args []string, stdout io.Writer) error {
	opts, rest, err := parseTokenOptions(args, false)
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return fmt.Errorf("unexpected argument %q", rest[0])
	}
	token, err := tokenFromOptions(opts)
	if err != nil {
		return err
	}

	fmt.Fprintln(stdout, token)
	return nil
}

func cmdCredential(args []string, stdin io.Reader, stdout io.Writer) error {
	opts, rest, err := parseTokenOptions(args, true)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return fmt.Errorf("credential requires one operation")
	}
	if rest[0] != "get" {
		return nil
	}

	fields, err := readCredential(stdin)
	if err != nil {
		return err
	}
	if fields["protocol"] != "https" {
		return nil
	}
	if fields["host"] != "github.com" {
		return nil
	}

	token, err := tokenFromOptions(opts)
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, "username=x-access-token")
	fmt.Fprintln(stdout, "password="+token)
	fmt.Fprintln(stdout)
	return nil
}

type tokenOptions struct {
	appID          string
	installationID string
	owner          string
	repo           string
	privateKeyPath string
	cachePath      string
	baseURL        string
	skew           time.Duration
	tokenCommand   []string
}

// print has no --token-command: a caller holding a command can run it
// directly, so only git, which needs the credential protocol, is offered one.
func parseTokenOptions(args []string, acceptCommand bool) (tokenOptions, []string, error) {
	fs := flag.NewFlagSet("print", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // caller reports the returned error itself
	appID := fs.String("app-id", "", "GitHub App id")
	installationID := fs.String("installation-id", "", "installation id")
	owner := fs.String("owner", "", "repository owner, resolved to an installation id together with --repo")
	repo := fs.String("repo", "", "repository name, resolved to an installation id together with --owner")
	privateKeyPath := fs.String("private-key-path", "", "path to the App's PEM private key")
	cachePath := fs.String("cache-path", "", "path to the token cache file")
	baseURL := fs.String("base-url", githubapp.DefaultBaseURL, "GitHub API base URL")
	skew := fs.Duration("skew", 5*time.Minute, "refresh the cached token this long before it actually expires")
	var tokenCommand *string
	if acceptCommand {
		tokenCommand = fs.String("token-command", "", "JSON argument array of a host command that prints one installation token line; replaces the App key flags")
	}
	if err := fs.Parse(args); err != nil {
		return tokenOptions{}, nil, err
	}

	if tokenCommand != nil && *tokenCommand != "" {
		if *privateKeyPath != "" {
			return tokenOptions{}, nil, fmt.Errorf("--token-command and --private-key-path are mutually exclusive")
		}
		command, err := tokencommand.Parse(*tokenCommand)
		if err != nil {
			return tokenOptions{}, nil, fmt.Errorf("--token-command: %w", err)
		}
		return tokenOptions{tokenCommand: command}, fs.Args(), nil
	}

	if *appID == "" {
		return tokenOptions{}, nil, fmt.Errorf("--app-id is required")
	}
	if *privateKeyPath == "" {
		return tokenOptions{}, nil, fmt.Errorf("--private-key-path is required")
	}
	if *cachePath == "" {
		return tokenOptions{}, nil, fmt.Errorf("--cache-path is required")
	}
	if *installationID == "" && (*owner == "" || *repo == "") {
		return tokenOptions{}, nil, fmt.Errorf("--installation-id, or both --owner and --repo, is required")
	}
	return tokenOptions{
		appID:          *appID,
		installationID: *installationID,
		owner:          *owner,
		repo:           *repo,
		privateKeyPath: *privateKeyPath,
		cachePath:      *cachePath,
		baseURL:        *baseURL,
		skew:           *skew,
	}, fs.Args(), nil
}

func tokenFromOptions(opts tokenOptions) (string, error) {
	if len(opts.tokenCommand) != 0 {
		return tokencommand.Run(opts.tokenCommand)
	}
	key, err := githubapp.LoadPrivateKey(opts.privateKeyPath)
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 30 * time.Second}
	cache := apptoken.NewCache(opts.cachePath)
	return githubapp.Token(cache, opts.skew, client, opts.baseURL, opts.appID, key, opts.installationID, opts.owner, opts.repo)
}

func readCredential(r io.Reader) (map[string]string, error) {
	fields := map[string]string{}
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			break
		}
		name, value, ok := strings.Cut(line, "=")
		if ok {
			fields[name] = value
		}
	}
	return fields, scanner.Err()
}
