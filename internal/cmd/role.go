package cmd

import "github.com/builtbyrobben/wpssh/internal/wpcli"

// Role commands

type RoleCmd struct {
	List   RoleListCmd   `cmd:"" help:"List roles"`
	Create RoleCreateCmd `cmd:"" help:"Create a role"`
	Delete RoleDeleteCmd `cmd:"" help:"Delete a role"`
	Exists RoleExistsCmd `cmd:"" help:"Check if role exists"`
}

type (
	RoleListCmd   struct{}
	RoleCreateCmd struct {
		Role        string `arg:"" help:"Role key"`
		DisplayName string `arg:"" help:"Display name"`
	}
)

type RoleDeleteCmd struct {
	Role string `arg:"" help:"Role key"`
}
type RoleExistsCmd struct {
	Role string `arg:"" help:"Role key"`
}

func (c *RoleListCmd) Run(g *Globals) error {
	return execPassthrough(g, wpcli.New("role", "list"))
}

func (c *RoleCreateCmd) Run(g *Globals) error {
	return execPassthrough(g, wpcli.New("role", "create").Arg(c.Role).Arg(c.DisplayName))
}

func (c *RoleDeleteCmd) Run(g *Globals) error {
	return execPassthrough(g, wpcli.New("role", "delete").Arg(c.Role))
}

func (c *RoleExistsCmd) Run(g *Globals) error {
	return execBoolCheck(g, "role", "exists", c.Role, "Role")
}
