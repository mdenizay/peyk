// Package setup implements peyk's resumable, step-based server provisioning.
//
// Each Step is idempotent and its outcome is recorded in the persistent state
// file, so an interrupted `peyk setup` resumes from the first unfinished step.
package setup

import (
	"context"
	"fmt"
	"time"

	"github.com/mdenizay/peyk/internal/config"
	"github.com/mdenizay/peyk/internal/i18n"
	"github.com/mdenizay/peyk/internal/sysinfo"
)

// Step is one selectable provisioning action.
type Step struct {
	ID       string
	Default  bool // pre-selected in the wizard
	Required bool // cannot be deselected
	Apply    func(ctx context.Context, env *Env) error
}

// Env carries shared context into step implementations.
type Env struct {
	Cfg  *config.Config
	Sys  sysinfo.Info
	Ver  string // peyk version, for systemd unit comments
}

// Name and Desc resolve the step's localized name/description.
func (s Step) Name() string { return i18n.T("step." + s.ID + ".name") }
func (s Step) Desc() string { return i18n.T("step." + s.ID + ".desc") }

// Steps returns all steps in execution order.
func Steps() []Step {
	return []Step{
		{ID: "system-update", Default: true, Apply: applySystemUpdate},
		{ID: "docker", Default: true, Required: true, Apply: applyDocker},
		{ID: "unattended-upgrades", Default: true, Apply: applyUnattendedUpgrades},
		{ID: "firewall", Default: true, Apply: applyFirewall},
		{ID: "fail2ban", Default: true, Apply: applyFail2ban},
		{ID: "ssh-hardening", Default: true, Apply: applySSHHardening},
		{ID: "sysctl-tuning", Default: true, Apply: applySysctl},
		{ID: "swap", Default: true, Apply: applySwap},
		{ID: "journald-limits", Default: false, Apply: applyJournaldLimits},
		{ID: "caddy-edge", Default: true, Required: true, Apply: applyCaddyEdge},
		{ID: "peyk-daemon", Default: true, Required: true, Apply: applyPeykDaemon},
		{ID: "auto-update", Default: false, Apply: applyAutoUpdate},
	}
}

// Run executes the selected steps, skipping ones already done, persisting
// progress after every step so an interrupted run resumes cleanly.
func Run(ctx context.Context, env *Env, selected []string) error {
	st, err := config.LoadState()
	if err != nil {
		return err
	}
	if st.SetupSteps == nil {
		st.SetupSteps = map[string]config.StepStatus{}
	}
	st.SetupSelected = selected
	if err := config.SaveState(st); err != nil {
		return err
	}

	sel := map[string]bool{}
	for _, id := range selected {
		sel[id] = true
	}

	steps := Steps()
	var todo []Step
	for _, s := range steps {
		if sel[s.ID] || s.Required {
			todo = append(todo, s)
		}
	}

	for i, s := range todo {
		if st.SetupSteps[s.ID].Status == "done" {
			fmt.Println(i18n.T("setup.step.skip", s.Name()))
			continue
		}
		fmt.Println(i18n.T("setup.step.start", i+1, len(todo), s.Name()))
		if err := s.Apply(ctx, env); err != nil {
			st.SetupSteps[s.ID] = config.StepStatus{Status: "failed", FinishedAt: now(), Error: err.Error()}
			_ = config.SaveState(st)
			fmt.Println(i18n.T("setup.step.fail", s.Name(), err))
			fmt.Println(i18n.T("setup.retry.hint"))
			return err
		}
		st.SetupSteps[s.ID] = config.StepStatus{Status: "done", FinishedAt: now()}
		if err := config.SaveState(st); err != nil {
			return err
		}
		fmt.Println(i18n.T("setup.step.done", s.Name()))
	}

	st.SetupComplete = true
	if err := config.SaveState(st); err != nil {
		return err
	}
	fmt.Println(i18n.T("setup.complete"))
	return nil
}

func now() string { return time.Now().UTC().Format(time.RFC3339) }
