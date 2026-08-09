package commands

import (
	"context"
	"log"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/kecbigmt/plect/app/internal/config"
	"github.com/kecbigmt/plect/app/internal/ghcache"
	"github.com/kecbigmt/plect/app/internal/service"
	"github.com/kecbigmt/plect/app/internal/state"
)

var (
	watchStatus   []string
	watchType     []string
	watchInterval time.Duration
)

var watchCmd = &cobra.Command{
	Use:   "watch",
	Short: "Poll GitHub data for tracked sessions and fire hooks on changes",
	Long: `Watch polls GitHub Issue/PR data for tracked sessions at a regular interval,
updates the local cache, detects changes (CI failures, review comments, state changes),
and fires post_sync_change hooks.

Use --status to filter by cached GitHub state (open, closed, merged).
Use --type to filter by URL type (issue, pr).
Use --interval to set the polling interval (default 60s).`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := config.Load()

		params := service.SyncParams{
			StatusFilter: watchStatus,
			TypeFilter:   watchType,
		}

		log.Printf("watching sessions every %s (status=%v, type=%v)", watchInterval, watchStatus, watchType)

		// Initial sync — establish baseline
		result, err := runSync(cfg, params)
		if err != nil {
			log.Printf("warning: initial sync failed: %v", err)
		} else {
			log.Printf("initial sync: %d fetched, %d skipped, %d changes", result.Fetched, result.Skipped, len(result.Changes))
		}

		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()

		ticker := time.NewTicker(watchInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				log.Println("shutting down watcher")
				return nil
			case <-ticker.C:
				result, err := runSync(cfg, params)
				if err != nil {
					log.Printf("warning: sync failed: %v", err)
					continue
				}
				if len(result.Changes) > 0 {
					log.Printf("detected %d change(s)", len(result.Changes))
					for _, c := range result.Changes {
						log.Printf("  %s: %s", c.SessionName, c.Summary)
					}
				}
			}
		}
	},
}

func runSync(cfg *config.Config, params service.SyncParams) (*service.SyncResult, error) {
	sessionStore := state.NewStore("")
	cacheStore := ghcache.NewCacheStore("")
	fetcher := ghcache.NewFetcher()

	return service.Sync(cfg, sessionStore, cacheStore, fetcher, params)
}

func init() {
	watchCmd.Flags().StringSliceVar(&watchStatus, "status", nil, "Filter by cached state: open, closed, merged (comma-separated)")
	watchCmd.Flags().StringSliceVar(&watchType, "type", nil, "Filter by URL type: issue, pr (comma-separated)")
	watchCmd.Flags().DurationVar(&watchInterval, "interval", 60*time.Second, "Polling interval (e.g. 60s, 5m)")
	rootCmd.AddCommand(watchCmd)
}
