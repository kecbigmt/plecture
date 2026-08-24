// gh-app-token mints and caches a GitHub App installation access token,
// composed by the github plugin's gh_app_guard effect into a `gh` wrapper
// (see plugins/github/scripts/gh-app-guard) — it never talks to `gh` itself,
// only to GitHub's App-authentication endpoints.
package main

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/kecbigmt/plecture/plugins/github/src/internal/apptoken"
	"github.com/kecbigmt/plecture/plugins/github/src/internal/githubapp"
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
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		// Every error reaching here is built from githubapp/apptoken, which
		// never fold key or token bytes into an error string — this prefix
		// is the only thing added at this layer.
		fmt.Fprintf(os.Stderr, "gh-app-token: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  gh-app-token print --app-id <id> (--installation-id <id> | --owner <owner> --repo <repo>) --private-key-path <path> --cache-path <path> [--base-url <url>] [--skew <duration>]`)
}

// cmdPrint mints (or reuses a cached, unexpired) installation token and
// writes exactly the token, newline-terminated, to stdout — nothing else.
// A caller capturing this command's stdout (the gh-app-guard wrapper does,
// via `$(...)`) gets the token and nothing it would need to strip.
func cmdPrint(args []string, stdout io.Writer) error {
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
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *appID == "" {
		return fmt.Errorf("--app-id is required")
	}
	if *privateKeyPath == "" {
		return fmt.Errorf("--private-key-path is required")
	}
	if *cachePath == "" {
		return fmt.Errorf("--cache-path is required")
	}
	if *installationID == "" && (*owner == "" || *repo == "") {
		return fmt.Errorf("--installation-id, or both --owner and --repo, is required")
	}

	key, err := githubapp.LoadPrivateKey(*privateKeyPath)
	if err != nil {
		return err
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resolvedInstallationID := *installationID

	token, err := apptoken.NewCache(*cachePath).Get(*skew, func() (string, time.Time, error) {
		jwt, err := githubapp.BuildJWT(*appID, key, time.Now())
		if err != nil {
			return "", time.Time{}, err
		}
		if resolvedInstallationID == "" {
			id, err := githubapp.ResolveInstallationID(client, *baseURL, jwt, *owner, *repo)
			if err != nil {
				return "", time.Time{}, err
			}
			resolvedInstallationID = id
		}
		minted, err := githubapp.MintInstallationToken(client, *baseURL, jwt, resolvedInstallationID)
		if err != nil {
			return "", time.Time{}, err
		}
		return minted.Token, minted.ExpiresAt, nil
	})
	if err != nil {
		return err
	}

	fmt.Fprintln(stdout, token)
	return nil
}
