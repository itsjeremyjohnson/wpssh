package cmd

import (
	"fmt"

	"github.com/builtbyrobben/wpssh/internal/wpcli"
)

// Menu commands

type MenuCmd struct {
	List   MenuListCmd   `cmd:"" help:"List menus"`
	Create MenuCreateCmd `cmd:"" help:"Create a menu"`
	Delete MenuDeleteCmd `cmd:"" help:"Delete a menu"`
	Item   MenuItemCmd   `cmd:"" help:"Manage menu items"`
}

type (
	MenuListCmd   struct{}
	MenuCreateCmd struct {
		Name string `arg:"" help:"Menu name"`
	}
)

type MenuDeleteCmd struct {
	ID int `arg:"" help:"Menu ID"`
}
type MenuItemCmd struct {
	List      MenuItemListCmd      `cmd:"" help:"List menu items"`
	AddPost   MenuItemAddPostCmd   `cmd:"" name:"add-post" help:"Add post to menu"`
	AddCustom MenuItemAddCustomCmd `cmd:"" name:"add-custom" help:"Add custom link to menu"`
	Update    MenuItemUpdateCmd    `cmd:"" help:"Update menu item"`
	Delete    MenuItemDeleteCmd    `cmd:"" help:"Delete menu item"`
}

type MenuItemListCmd struct {
	Menu string `arg:"" help:"Menu name or ID"`
}
type MenuItemAddPostCmd struct {
	Menu string `arg:"" help:"Menu name or ID"`
	ID   int    `arg:"" help:"Post ID"`
}
type MenuItemAddCustomCmd struct {
	Menu  string `arg:"" help:"Menu name or ID"`
	Title string `arg:"" help:"Link title"`
	URL   string `arg:"" help:"Link URL"`
}
type MenuItemUpdateCmd struct {
	ID int `arg:"" help:"Item ID"`
}
type MenuItemDeleteCmd struct {
	ID int `arg:"" help:"Item ID"`
}

func (c *MenuListCmd) Run(g *Globals) error {
	return execPassthrough(g, wpcli.New("menu", "list"))
}

func (c *MenuCreateCmd) Run(g *Globals) error {
	return execPassthrough(g, wpcli.New("menu", "create"), c.Name)
}

func (c *MenuDeleteCmd) Run(g *Globals) error {
	return execPassthrough(g, wpcli.New("menu", "delete"), fmt.Sprint(c.ID))
}

func (c *MenuItemListCmd) Run(g *Globals) error {
	return execPassthrough(g, wpcli.New("menu", "item", "list"), c.Menu)
}

func (c *MenuItemAddPostCmd) Run(g *Globals) error {
	return execPassthrough(g, wpcli.New("menu", "item", "add-post"), c.Menu, fmt.Sprint(c.ID))
}

func (c *MenuItemAddCustomCmd) Run(g *Globals) error {
	return execPassthrough(g, wpcli.New("menu", "item", "add-custom"), c.Menu, c.Title, c.URL)
}

func (c *MenuItemUpdateCmd) Run(g *Globals) error {
	return execPassthrough(g, wpcli.New("menu", "item", "update"), fmt.Sprint(c.ID))
}

func (c *MenuItemDeleteCmd) Run(g *Globals) error {
	return execPassthrough(g, wpcli.New("menu", "item", "delete"), fmt.Sprint(c.ID))
}
