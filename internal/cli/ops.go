package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"

	"github.com/mdenizay/peyk/internal/caddy"
	"github.com/mdenizay/peyk/internal/daemon"
	"github.com/mdenizay/peyk/internal/deploy"
	"github.com/mdenizay/peyk/internal/execx"
	"github.com/mdenizay/peyk/internal/i18n"
	"github.com/mdenizay/peyk/internal/project"
	"github.com/mdenizay/peyk/internal/update"
)

func newDeployCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "deploy <project>",
		Short: "Deploy a project's configured branch now",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := project.Load(args[0])
			if err != nil {
				return err
			}
			return deploy.Run(cmd.Context(), cfg, p)
		},
	}
}

func newListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List projects and their status",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return printList(cmd.Context())
		},
	}
}

func printList(ctx context.Context) error {
	projects, err := project.List()
	if err != nil {
		return err
	}
	if len(projects) == 0 {
		fmt.Println(i18n.T("list.empty"))
		return nil
	}
	fmt.Println(i18n.T("list.header"))
	for _, p := range projects {
		status := i18n.T("stopped")
		if p.ActiveSlot != "" {
			out, _ := execx.Output(ctx, "docker", "inspect", "-f", "{{.State.Status}}", p.SlotContainerHost(p.ActiveSlot))
			if out == "running" {
				status = i18n.T("running")
			}
		}
		fmt.Printf("%-14s %-11s %-10s %s\n", p.Name, p.Framework, status, strings.Join(p.Domains, ", "))
	}
	return nil
}

func newLogsCmd() *cobra.Command {
	var follow bool
	var tail string
	cmd := &cobra.Command{
		Use:   "logs <project> [service]",
		Short: "Show a project's container logs (default: the app)",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := project.Load(args[0])
			if err != nil {
				return err
			}
			svc := "app_" + p.ActiveSlot
			composeArgs := []string{"compose", "--project-directory", p.Dir()}
			if p.ActiveSlot != "" {
				composeArgs = append(composeArgs, "--profile", p.ActiveSlot)
			}
			if len(args) == 2 {
				svc = args[1]
			}
			composeArgs = append(composeArgs, "logs", "--tail", tail)
			if follow {
				composeArgs = append(composeArgs, "-f")
			}
			composeArgs = append(composeArgs, svc)
			return execx.Run(cmd.Context(), "docker", composeArgs...)
		},
	}
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "follow log output")
	cmd.Flags().StringVar(&tail, "tail", "200", "lines to show from the end")
	return cmd
}

func newEnvCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "env <project>",
		Short: "Edit the project's application .env ($EDITOR)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := project.Load(args[0])
			if err != nil {
				return err
			}
			editor := os.Getenv("EDITOR")
			if editor == "" {
				editor = "nano"
			}
			envPath := p.SharedDir() + "/.env"
			c := exec.CommandContext(cmd.Context(), editor, envPath)
			c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
			if err := c.Run(); err != nil {
				return err
			}
			fmt.Printf("Changes apply on the next deploy: peyk deploy %s\n", p.Name)
			return nil
		},
	}
}

func newRemoveCmd() *cobra.Command {
	var keepData bool
	cmd := &cobra.Command{
		Use:   "remove <project>",
		Short: "Remove a project (stops containers; --keep-data preserves volumes and files)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := project.Load(args[0])
			if err != nil {
				return err
			}
			var ok bool
			if err := huh.NewConfirm().
				Title(fmt.Sprintf("Remove project %q? Domains: %s", p.Name, strings.Join(p.Domains, ", "))).
				Value(&ok).Run(); err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("%s", i18n.T("cancelled"))
			}
			downArgs := []string{"compose", "--project-directory", p.Dir(), "--profile", "blue", "--profile", "green", "down"}
			if !keepData {
				downArgs = append(downArgs, "-v")
			}
			_ = execx.Run(cmd.Context(), "docker", downArgs...)
			if err := caddy.RemoveSite(p.Name); err != nil {
				return err
			}
			_ = caddy.Reload(cmd.Context())
			if !keepData {
				if err := os.RemoveAll(p.Dir()); err != nil {
					return err
				}
				os.Remove(p.KeyPath())
				os.Remove(p.KeyPath() + ".pub")
			}
			fmt.Println(i18n.T("done"))
			return nil
		},
	}
	cmd.Flags().BoolVar(&keepData, "keep-data", false, "keep volumes, releases and env files on disk")
	return cmd
}

func newServeCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "serve",
		Short:  "Run the webhook daemon (used by systemd)",
		Hidden: true,
		RunE: func(*cobra.Command, []string) error {
			return daemon.Serve(cfg)
		},
	}
}

func newSelfUpdateCmd() *cobra.Command {
	var ifNeeded bool
	cmd := &cobra.Command{
		Use:   "self-update",
		Short: "Update peyk from GitHub Releases (checksum-verified)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return update.Run(cmd.Context(), cfg, appVersion, ifNeeded)
		},
	}
	cmd.Flags().BoolVar(&ifNeeded, "if-needed", false, "exit quietly when already up to date")
	return cmd
}

// runDashboard is the bare `peyk` entry: a small interactive menu.
func runDashboard(ctx context.Context) error {
	for {
		if err := printList(ctx); err != nil {
			return err
		}
		var choice string
		if err := huh.NewSelect[string]().
			Title("peyk").
			Options(
				huh.NewOption(i18n.T("new.title"), "new"),
				huh.NewOption("Deploy", "deploy"),
				huh.NewOption("Setup", "setup"),
				huh.NewOption("Quit", "quit"),
			).Value(&choice).Run(); err != nil {
			return err
		}
		switch choice {
		case "new":
			if err := runNewWizard(ctx); err != nil {
				fmt.Fprintln(os.Stderr, err)
			}
		case "deploy":
			projects, err := project.List()
			if err != nil || len(projects) == 0 {
				fmt.Println(i18n.T("list.empty"))
				continue
			}
			var opts []huh.Option[string]
			for _, p := range projects {
				opts = append(opts, huh.NewOption(p.Name, p.Name))
			}
			var name string
			if err := huh.NewSelect[string]().Title("Deploy").Options(opts...).Value(&name).Run(); err != nil {
				return err
			}
			p, err := project.Load(name)
			if err != nil {
				return err
			}
			if err := deploy.Run(ctx, cfg, p); err != nil {
				fmt.Fprintln(os.Stderr, err)
			}
		case "setup":
			fmt.Println("Run: sudo peyk setup")
		default:
			return nil
		}
	}
}
