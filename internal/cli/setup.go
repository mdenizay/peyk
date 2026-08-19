package cli

import (
	"fmt"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"

	"github.com/mdenizay/peyk/internal/config"
	"github.com/mdenizay/peyk/internal/i18n"
	"github.com/mdenizay/peyk/internal/setup"
	"github.com/mdenizay/peyk/internal/sysinfo"
)

func newSetupCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Provision this server (resumable; safe to re-run)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			sys := sysinfo.Detect()
			if !sysinfo.IsRoot() {
				return fmt.Errorf("%s", i18n.T("setup.need.root"))
			}
			if !sys.SupportedUbuntu() {
				return fmt.Errorf("%s", i18n.T("setup.need.ubuntu"))
			}

			st, err := config.LoadState()
			if err != nil {
				return err
			}

			// Resume path: a previous run selected steps already.
			if len(st.SetupSelected) > 0 && !st.SetupComplete {
				fmt.Println(i18n.T("setup.resume"))
				env := &setup.Env{Cfg: &cfg, Sys: sys, Ver: appVersion}
				return setup.Run(cmd.Context(), env, st.SetupSelected)
			}

			// Fresh run: language, e-mail, step selection.
			if cfg.Language == "" || !st.SetupComplete && len(st.SetupSelected) == 0 {
				lang := cfg.Language
				if lang == "" {
					lang = "en"
				}
				if !yes {
					if err := huh.NewSelect[string]().
						Title(i18n.T("setup.pick.lang")).
						Options(
							huh.NewOption("English", "en"),
							huh.NewOption("Türkçe", "tr"),
						).
						Value(&lang).Run(); err != nil {
						return err
					}
				}
				cfg.Language = lang
				i18n.SetLang(lang)
			}

			if cfg.ACMEEmail == "" && !yes {
				if err := huh.NewInput().
					Title(i18n.T("setup.email.prompt")).
					Value(&cfg.ACMEEmail).Run(); err != nil {
					return err
				}
			}
			if err := config.Save(cfg); err != nil {
				return err
			}

			steps := setup.Steps()
			var selected []string
			if yes {
				for _, s := range steps {
					if s.Default || s.Required {
						selected = append(selected, s.ID)
					}
				}
			} else {
				var opts []huh.Option[string]
				var defaults []string
				for _, s := range steps {
					label := s.Name()
					if s.Required {
						label += " *"
					}
					opts = append(opts, huh.NewOption(label+" — "+s.Desc(), s.ID))
					if s.Default || s.Required {
						defaults = append(defaults, s.ID)
					}
				}
				selected = defaults
				if err := huh.NewMultiSelect[string]().
					Title(i18n.T("setup.title")).
					Description(i18n.T("setup.pick.steps")).
					Options(opts...).
					Height(len(opts) + 4).
					Value(&selected).Run(); err != nil {
					return err
				}
			}

			env := &setup.Env{Cfg: &cfg, Sys: sys, Ver: appVersion}
			return setup.Run(cmd.Context(), env, selected)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "non-interactive: apply all default steps")
	return cmd
}
