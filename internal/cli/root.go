// Package cli wires up peyk's cobra command tree.
package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/mdenizay/peyk/internal/config"
	"github.com/mdenizay/peyk/internal/i18n"
)

var (
	appVersion string
	cfg        config.Config
)

// Execute runs the CLI.
func Execute(version string) error {
	appVersion = version

	root := &cobra.Command{
		Use:           "peyk",
		Short:         "Deploy Laravel & Next.js apps on Ubuntu with isolation, auto-SSL and zero downtime",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			var err error
			cfg, err = config.Load()
			if err != nil {
				return err
			}
			i18n.SetLang(cfg.Language)
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDashboard(cmd.Context())
		},
	}

	root.AddCommand(
		newSetupCmd(),
		newNewCmd(),
		newDeployCmd(),
		newListCmd(),
		newLogsCmd(),
		newEnvCmd(),
		newCloudflareCmd(),
		newRemoveCmd(),
		newServeCmd(),
		newSelfUpdateCmd(),
		newVersionCmd(),
	)
	return root.Execute()
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print peyk's version",
		Run: func(*cobra.Command, []string) {
			fmt.Println("peyk", appVersion)
		},
	}
}
