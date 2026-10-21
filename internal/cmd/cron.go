package cmd

import (
	"context"
	"fmt"

	"github.com/builtbyrobben/wpssh/internal/wpcli"
)

// Cron commands

type CronCmd struct {
	Event    CronEventCmd    `cmd:"" help:"Manage cron events"`
	Schedule CronScheduleCmd `cmd:"" help:"Manage cron schedules"`
	Test     CronTestCmd     `cmd:"" help:"Test WP-Cron spawning"`
}

type CronEventCmd struct {
	List       CronEventListCmd       `cmd:"" help:"List events"`
	Run        CronEventRunCmd        `cmd:"" help:"Run an event"`
	Schedule   CronEventScheduleCmd   `cmd:"" help:"Schedule an event"`
	Unschedule CronEventUnscheduleCmd `cmd:"" help:"Unschedule an event"`
	Delete     CronEventDeleteCmd     `cmd:"" help:"Delete an event"`
}

type (
	CronEventListCmd struct{}
	CronEventRunCmd  struct {
		Hook string `arg:"" help:"Event hook"`
	}
)

type CronEventScheduleCmd struct {
	Hook       string `arg:"" help:"Event hook"`
	NextRun    string `arg:"" help:"Next run time"`
	Recurrence string `arg:"" optional:"" help:"Recurrence"`
}
type CronEventUnscheduleCmd struct {
	Hook string `arg:"" help:"Event hook"`
}
type CronEventDeleteCmd struct {
	Hook string `arg:"" help:"Event hook"`
}

type CronScheduleCmd struct {
	List CronScheduleListCmd `cmd:"" help:"List schedules"`
}

type (
	CronScheduleListCmd struct{}
	CronTestCmd         struct{}
)

func (c *CronEventListCmd) Run(g *Globals) error {
	rc, err := NewRunContext(g)
	if err != nil {
		return err
	}
	defer rc.Close()
	site, err := rc.ResolveSite()
	if err != nil {
		return err
	}

	result, err := rc.ExecWP(context.Background(), site,
		wpcli.New("cron", "event", "list").Format("json").Build(site.WPPath))
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("wp cron event list: %s", result.Stderr)
	}
	events, err := wpcli.ParseJSON[wpcli.CronEvent](result.Stdout)
	if err != nil {
		return err
	}
	return rc.Formatter.Format(events)
}

func (c *CronEventRunCmd) Run(g *Globals) error {
	return execPassthrough(g, wpcli.New("cron", "event", "run").Arg(c.Hook))
}

func (c *CronEventScheduleCmd) Run(g *Globals) error {
	cmd := wpcli.New("cron", "event", "schedule").Arg(c.Hook).Arg(c.NextRun)
	if c.Recurrence != "" {
		cmd.Arg(c.Recurrence)
	}
	return execPassthrough(g, cmd)
}

func (c *CronEventUnscheduleCmd) Run(g *Globals) error {
	return execPassthrough(g, wpcli.New("cron", "event", "unschedule").Arg(c.Hook))
}

func (c *CronEventDeleteCmd) Run(g *Globals) error {
	return execPassthrough(g, wpcli.New("cron", "event", "delete").Arg(c.Hook))
}

func (c *CronScheduleListCmd) Run(g *Globals) error {
	rc, err := NewRunContext(g)
	if err != nil {
		return err
	}
	defer rc.Close()
	site, err := rc.ResolveSite()
	if err != nil {
		return err
	}

	result, err := rc.ExecWP(context.Background(), site,
		wpcli.New("cron", "schedule", "list").Format("json").Build(site.WPPath))
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("wp cron schedule list: %s", result.Stderr)
	}
	schedules, err := wpcli.ParseJSON[wpcli.CronSchedule](result.Stdout)
	if err != nil {
		return err
	}
	return rc.Formatter.Format(schedules)
}

func (c *CronTestCmd) Run(g *Globals) error {
	return execPassthrough(g, wpcli.New("cron", "test"))
}
