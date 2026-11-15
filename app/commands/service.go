package commands

import (
	"fmt"
	"sort"
	"text/tabwriter"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/plugins"
	"github.com/kecbigmt/plecture/app/internal/pluginservice"
	"github.com/spf13/cobra"
)

var serviceListAll bool

var serviceCmd = &cobra.Command{Use: "service", Short: "Inspect declared plugin services"}

var serviceListCmd = &cobra.Command{
	Use: "list", Short: "List declared services", Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		decls, err := pluginservice.BuildDeclarations(cfg.Plugins, nil)
		if err != nil {
			return err
		}
		statuses, err := pluginservice.ReadStatusSnapshot(pluginservice.DefaultStatusSnapshotPath())
		if err != nil {
			return err
		}
		rows := serviceRows(cfg.Plugins, decls, statuses, serviceListAll)
		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tEXECUTABLE\tSTATE\tPID\tRESTARTS")
		for _, row := range rows {
			pid, restarts := "", ""
			if row.PID > 0 {
				pid = fmt.Sprint(row.PID)
			}
			if row.Name != "" {
				restarts = fmt.Sprint(row.Restarts)
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", row.Name, row.Executable, row.State, pid, restarts)
		}
		return w.Flush()
	},
}

type serviceRow struct {
	Name, Executable, State string
	PID, Restarts           int
}

func serviceRows(mounted []plugins.Mounted, decls []pluginservice.Declaration, statuses []pluginservice.Status, all bool) []serviceRow {
	byID := make(map[string]pluginservice.Status, len(statuses))
	for _, st := range statuses {
		byID[st.ID] = st
	}
	bound := make(map[string]bool, len(decls))
	rows := make([]serviceRow, 0, len(decls))
	for _, decl := range decls {
		bound[decl.Executable] = true
		st := byID[decl.ID]
		state := "stopped"
		if st.Running {
			state = "running"
		}
		rows = append(rows, serviceRow{Name: decl.Name, Executable: decl.Executable, State: state, PID: st.PID, Restarts: st.RestartCount})
	}
	if all {
		for _, m := range mounted {
			for _, ex := range m.Manifest.Executables {
				id := m.ID + "/" + ex.Name
				if ex.Service && !bound[id] {
					rows = append(rows, serviceRow{Executable: id, State: "available"})
				}
			}
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Name == "" || rows[j].Name == "" {
			if rows[i].Name == "" && rows[j].Name == "" {
				return rows[i].Executable < rows[j].Executable
			}
			return rows[j].Name == ""
		}
		return rows[i].Name < rows[j].Name
	})
	return rows
}

func init() {
	serviceListCmd.Flags().BoolVar(&serviceListAll, "all", false, "Include service-capable executables without a declaration")
	serviceCmd.AddCommand(serviceListCmd)
	rootCmd.AddCommand(serviceCmd)
}
