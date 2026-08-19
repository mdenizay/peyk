package cli

import (
	"fmt"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"

	"github.com/mdenizay/peyk/internal/caddy"
	"github.com/mdenizay/peyk/internal/project"
)

func newCloudflareCmd() *cobra.Command {
	var token string
	cmd := &cobra.Command{
		Use:   "cloudflare <project>",
		Short: "Serve a project behind Cloudflare's proxy (DNS-01 certificates)",
		Long: `Switches the project's TLS to Let's Encrypt DNS-01 challenges via the
Cloudflare API, which works while Cloudflare's proxy (orange cloud) is on.

Create an API token at Cloudflare → My Profile → API Tokens with:
  Zone / Zone / Read  and  Zone / DNS / Edit  (scoped to your zone)

Then set Cloudflare SSL/TLS mode to "Full (strict)" for the domain.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := project.Load(args[0])
			if err != nil {
				return err
			}

			if token == "" && !caddy.CloudflareEnabled() {
				if err := huh.NewInput().
					Title("Cloudflare API token (Zone:Read + DNS:Edit):").
					EchoMode(huh.EchoModePassword).
					Value(&token).Run(); err != nil {
					return err
				}
			}
			if token != "" {
				if err := caddy.SetCloudflareToken(token); err != nil {
					return err
				}
			}

			p.TLSMode = "cloudflare-dns"
			if err := p.Save(); err != nil {
				return err
			}
			if p.ActiveSlot != "" {
				if err := caddy.WriteSite(p, p.ActiveSlot); err != nil {
					return err
				}
			}

			// Rebuilds Caddy with the cloudflare DNS module (first run takes a
			// few minutes) and restarts it with the new config.
			fmt.Println("Rebuilding the edge proxy with the Cloudflare DNS module…")
			if err := caddy.EnsureEdgeStack(cmd.Context(), cfg.ACMEEmail); err != nil {
				return err
			}
			fmt.Printf("Done. %s now uses DNS-01 certificates — the Cloudflare proxy (orange cloud) can stay on.\n", p.Name)
			fmt.Println("Reminder: set the domain's SSL/TLS mode to \"Full (strict)\" in Cloudflare.")
			return nil
		},
	}
	cmd.Flags().StringVar(&token, "token", "", "Cloudflare API token (prompted if not given and not stored)")
	return cmd
}
