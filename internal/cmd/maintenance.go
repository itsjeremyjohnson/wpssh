package cmd

import "github.com/builtbyrobben/wpssh/internal/wpcli"

// Maintenance commands

type MaintenanceCmd struct {
	Activate   MaintenanceActivateCmd   `cmd:"" help:"Activate maintenance mode"`
	Deactivate MaintenanceDeactivateCmd `cmd:"" help:"Deactivate maintenance mode"`
	Status     MaintenanceStatusCmd     `cmd:"" help:"Show maintenance mode status"`
}

type (
	MaintenanceActivateCmd   struct{}
	MaintenanceDeactivateCmd struct{}
	MaintenanceStatusCmd     struct{}
)

func (c *MaintenanceActivateCmd) Run(g *Globals) error {
	return execPassthrough(g, wpcli.New("maintenance-mode", "activate"))
}

func (c *MaintenanceDeactivateCmd) Run(g *Globals) error {
	return execPassthrough(g, wpcli.New("maintenance-mode", "deactivate"))
}

func (c *MaintenanceStatusCmd) Run(g *Globals) error {
	return execPassthrough(g, wpcli.New("maintenance-mode", "status"))
}
