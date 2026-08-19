package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"

	"github.com/mdenizay/peyk/internal/deploy"
	"github.com/mdenizay/peyk/internal/gitx"
	"github.com/mdenizay/peyk/internal/githubx"
	"github.com/mdenizay/peyk/internal/i18n"
	"github.com/mdenizay/peyk/internal/project"
)

func newNewCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "new",
		Short: "Create a project (interactive wizard)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runNewWizard(cmd.Context())
		},
	}
}

func runNewWizard(ctx context.Context) error {
	gh := githubx.New(cfg.GitHubToken)

	// 1. Repository: pick from GitHub when a token exists, else type it.
	var repoURL, defaultBranch string
	if gh != nil {
		repos, err := gh.ListRepos(ctx, 50)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: repo listing failed (%v), falling back to manual entry\n", err)
		} else if len(repos) > 0 {
			var opts []huh.Option[string]
			for _, r := range repos {
				label := r.FullName
				if r.Private {
					label += " (private)"
				}
				opts = append(opts, huh.NewOption(label, r.SSHURL+"|"+r.DefaultBranch))
			}
			var pick string
			if err := huh.NewSelect[string]().
				Title(i18n.T("new.repo.prompt")).
				Options(opts...).Height(15).
				Value(&pick).Run(); err != nil {
				return err
			}
			parts := strings.SplitN(pick, "|", 2)
			repoURL = parts[0]
			if len(parts) == 2 {
				defaultBranch = parts[1]
			}
		}
	}
	if repoURL == "" {
		var raw string
		if err := huh.NewInput().Title(i18n.T("new.repo.prompt")).Value(&raw).Run(); err != nil {
			return err
		}
		var err error
		repoURL, err = project.NormalizeRepo(raw)
		if err != nil {
			return err
		}
	}
	if defaultBranch == "" {
		defaultBranch = "main"
	}

	// 2. Name & branch.
	name := suggestName(repoURL)
	if err := huh.NewInput().Title(i18n.T("new.name.prompt")).Value(&name).
		Validate(func(s string) error {
			if !project.ValidName(s) {
				return fmt.Errorf("a-z0-9- only")
			}
			if _, err := project.Load(s); err == nil {
				return fmt.Errorf("project exists")
			}
			return nil
		}).Run(); err != nil {
		return err
	}
	branch := defaultBranch
	if err := huh.NewInput().Title(i18n.T("new.branch.prompt")).Value(&branch).Run(); err != nil {
		return err
	}

	// 3. Domains.
	var domainsRaw string
	if err := huh.NewInput().Title(i18n.T("new.domains.prompt")).
		Placeholder("example.com, www.example.com").
		Value(&domainsRaw).Run(); err != nil {
		return err
	}
	var domains []string
	for _, d := range strings.Split(domainsRaw, ",") {
		if d = strings.TrimSpace(d); d != "" {
			domains = append(domains, d)
		}
	}
	if len(domains) == 0 {
		return fmt.Errorf("at least one domain is required")
	}

	p := &project.Project{
		Name:          name,
		Repo:          repoURL,
		Branch:        branch,
		Domains:       domains,
		WebhookSecret: project.NewSecret(),
	}

	// 4. Deploy key — must be on GitHub before the first fetch.
	pubKey, err := gitx.EnsureDeployKey(ctx, p)
	if err != nil {
		return err
	}
	ownerRepo := project.OwnerRepo(repoURL)
	keyAdded := false
	if gh != nil && ownerRepo != "" {
		if err := gh.AddDeployKey(ctx, ownerRepo, "peyk: "+name, pubKey); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not add deploy key automatically: %v\n", err)
		} else {
			fmt.Println(i18n.T("new.deploykey.added"))
			keyAdded = true
		}
	}
	if !keyAdded {
		fmt.Println("\n" + i18n.T("new.deploykey.title"))
		fmt.Println("\n  " + strings.TrimSpace(pubKey) + "\n")
		var ok bool
		if err := huh.NewConfirm().Title(i18n.T("continue")).Value(&ok).Run(); err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("%s", i18n.T("cancelled"))
		}
	}

	// 5. First fetch → framework detection.
	if err := p.Save(); err != nil {
		return err
	}
	releaseDir, _, err := gitx.FetchAndCheckout(ctx, p)
	if err != nil {
		return err
	}
	p.Framework = project.DetectFramework(releaseDir)
	fmt.Println(i18n.T("new.framework.found", p.Framework))
	project.DefaultsFor(p)

	// 6. Services.
	svc := project.Services{Postgres: true, Redis: true}
	if p.Framework == project.Laravel {
		svc = project.DetectLaravelExtras(releaseDir)
		svc.Postgres, svc.Redis = true, true
	}
	var picked []string
	preselect := func(id string, on bool) {
		if on {
			picked = append(picked, id)
		}
	}
	preselect("postgres", svc.Postgres)
	preselect("redis", svc.Redis)
	if p.Framework == project.Laravel {
		preselect("queue", svc.Queue)
		preselect("scheduler", svc.Scheduler)
		preselect("reverb", svc.Reverb)
	}
	opts := []huh.Option[string]{
		huh.NewOption("PostgreSQL", "postgres"),
		huh.NewOption("Redis", "redis"),
	}
	if p.Framework == project.Laravel {
		opts = append(opts,
			huh.NewOption("Queue worker", "queue"),
			huh.NewOption("Scheduler (cron)", "scheduler"),
			huh.NewOption("Reverb (websockets)", "reverb"),
		)
	}
	if err := huh.NewMultiSelect[string]().
		Title(i18n.T("new.services.prompt")).
		Options(opts...).Value(&picked).Run(); err != nil {
		return err
	}
	p.Services = project.Services{}
	for _, s := range picked {
		switch s {
		case "postgres":
			p.Services.Postgres = true
			p.DBPassword = project.NewSecret()[:32]
		case "redis":
			p.Services.Redis = true
			p.RedisPassword = project.NewSecret()[:32]
		case "queue":
			p.Services.Queue = true
		case "scheduler":
			p.Services.Scheduler = true
		case "reverb":
			p.Services.Reverb = true
		}
	}
	if err := p.Save(); err != nil {
		return err
	}

	// 7. Webhook.
	hookURL := fmt.Sprintf("https://%s/_peyk/hooks/%s", domains[0], name)
	hookAdded := false
	if gh != nil && ownerRepo != "" {
		if err := gh.AddWebhook(ctx, ownerRepo, hookURL, p.WebhookSecret); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not add webhook automatically: %v\n", err)
		} else {
			fmt.Println(i18n.T("new.webhook.added"))
			hookAdded = true
		}
	}
	if !hookAdded {
		fmt.Println("\n" + i18n.T("new.webhook.title"))
		fmt.Printf("\n  URL:    %s\n  Secret: %s\n  Events: push (application/json)\n\n", hookURL, p.WebhookSecret)
	}

	// 8. First deploy.
	fmt.Println(i18n.T("new.created", name))
	return deploy.Run(ctx, cfg, p)
}

func suggestName(repoURL string) string {
	or := project.OwnerRepo(repoURL)
	if or == "" {
		return ""
	}
	_, name, _ := strings.Cut(or, "/")
	name = strings.ToLower(strings.TrimSuffix(name, ".git"))
	name = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
			return r
		}
		return '-'
	}, name)
	return strings.Trim(name, "-")
}
