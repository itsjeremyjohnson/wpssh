package cmd

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/builtbyrobben/wpssh/internal/cache"
	"github.com/builtbyrobben/wpssh/internal/wpcli"
)

// User commands

type UserCmd struct {
	List          UserListCmd          `cmd:"" help:"List users"`
	Create        UserCreateCmd        `cmd:"" help:"Create a user"`
	Delete        UserDeleteCmd        `cmd:"" help:"Delete a user"`
	Get           UserGetCmd           `cmd:"" help:"Get user details"`
	Update        UserUpdateCmd        `cmd:"" help:"Update a user"`
	SetRole       UserSetRoleCmd       `cmd:"" name:"set-role" help:"Set user role"`
	ResetPassword UserResetPasswordCmd `cmd:"" name:"reset-password" help:"Reset password"`
	Exists        UserExistsCmd        `cmd:"" help:"Check if user exists"`
	Meta          UserMetaCmd          `cmd:"" help:"User meta management"`
}

type UserListCmd struct {
	Role string `help:"Filter by role"`
}
type UserCreateCmd struct {
	Login string `arg:"" help:"User login"`
	Email string `arg:"" help:"User email"`
	Role  string `help:"User role" default:"subscriber"`
}
type UserDeleteCmd struct {
	User string `arg:"" help:"User ID or login"`
}
type UserGetCmd struct {
	User string `arg:"" help:"User ID or login"`
}
type UserUpdateCmd struct {
	User string `arg:"" help:"User ID or login"`
}
type UserSetRoleCmd struct {
	User string `arg:"" help:"User ID or login"`
	Role string `arg:"" help:"Role to assign"`
}
type UserResetPasswordCmd struct {
	User string `arg:"" help:"User ID or login"`
}
type UserExistsCmd struct {
	User string `arg:"" help:"User ID or login"`
}
type UserMetaCmd struct {
	List   UserMetaListCmd   `cmd:"" help:"List user meta"`
	Get    UserMetaGetCmd    `cmd:"" help:"Get user meta"`
	Update UserMetaUpdateCmd `cmd:"" help:"Update user meta"`
	Delete UserMetaDeleteCmd `cmd:"" help:"Delete user meta"`
}
type UserMetaListCmd struct {
	User string `arg:"" help:"User ID"`
}
type UserMetaGetCmd struct {
	User string `arg:"" help:"User ID"`
	Key  string `arg:"" help:"Meta key"`
}
type UserMetaUpdateCmd struct {
	User  string `arg:"" help:"User ID"`
	Key   string `arg:"" help:"Meta key"`
	Value string `arg:"" help:"Meta value"`
}
type UserMetaDeleteCmd struct {
	User string `arg:"" help:"User ID"`
	Key  string `arg:"" help:"Meta key"`
}

func (c *UserListCmd) Run(g *Globals) error {
	rc, err := NewRunContext(g)
	if err != nil {
		return err
	}
	defer rc.Close()
	site, err := rc.ResolveSite()
	if err != nil {
		return err
	}

	builder := wpcli.New("user", "list").Format("json")
	if c.Role != "" {
		builder.Flag("role", c.Role)
	}
	cacheKey := builder.CacheKey()

	// Check cache first.
	if cached := rc.CacheGet(site.Alias, cacheKey); cached != "" {
		users, err := wpcli.ParseJSON[wpcli.User](cached)
		if err == nil {
			return rc.Formatter.Format(users)
		}
	}

	result, err := rc.ExecWP(context.Background(), site, builder.Build(site.WPPath))
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("wp user list: %s", result.Stderr)
	}

	users, err := wpcli.ParseJSON[wpcli.User](result.Stdout)
	if err != nil {
		return err
	}

	// Store in cache.
	if data, err := json.Marshal(users); err == nil {
		rc.CacheSet(site.Alias, cacheKey, "user list", string(data), cache.CategoryUsers, cache.TTLUsers)
	}

	return rc.Formatter.Format(users)
}

func (c *UserCreateCmd) Run(g *Globals) error {
	rc, err := NewRunContext(g)
	if err != nil {
		return err
	}
	defer rc.Close()
	site, err := rc.ResolveSite()
	if err != nil {
		return err
	}

	builder := wpcli.New("user", "create").Arg(c.Login).Arg(c.Email)
	if c.Role != "" {
		builder.Flag("role", c.Role)
	}
	result, err := rc.ExecWP(context.Background(), site, builder.Build(site.WPPath))
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("wp user create: %s", result.Stderr)
	}
	rc.CacheInvalidate(site.Alias, []string{cache.CategoryUsers, cache.CategorySnapshot})
	fmt.Fprint(rc.Stdout, result.Stdout)
	return nil
}

func (c *UserDeleteCmd) Run(g *Globals) error {
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
		wpcli.New("user", "delete").Arg(c.User).Build(site.WPPath))
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("wp user delete: %s", result.Stderr)
	}
	rc.CacheInvalidate(site.Alias, []string{cache.CategoryUsers, cache.CategorySnapshot})
	fmt.Fprint(rc.Stdout, result.Stdout)
	return nil
}

func (c *UserGetCmd) Run(g *Globals) error {
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
		wpcli.New("user", "get").Arg(c.User).Format("json").Build(site.WPPath))
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("wp user get: %s", result.Stderr)
	}
	user, err := wpcli.ParseSingle[wpcli.User](result.Stdout)
	if err != nil {
		return err
	}
	return rc.Formatter.Format(user)
}

func (c *UserUpdateCmd) Run(g *Globals) error {
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
		wpcli.New("user", "update").Arg(c.User).Build(site.WPPath))
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("wp user update: %s", result.Stderr)
	}
	rc.CacheInvalidate(site.Alias, []string{cache.CategoryUsers})
	fmt.Fprint(rc.Stdout, result.Stdout)
	return nil
}

func (c *UserSetRoleCmd) Run(g *Globals) error {
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
		wpcli.New("user", "set-role").Arg(c.User).Arg(c.Role).Build(site.WPPath))
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("wp user set-role: %s", result.Stderr)
	}
	rc.CacheInvalidate(site.Alias, []string{cache.CategoryUsers})
	fmt.Fprint(rc.Stdout, result.Stdout)
	return nil
}

func (c *UserResetPasswordCmd) Run(g *Globals) error {
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
		wpcli.New("user", "reset-password").Arg(c.User).Build(site.WPPath))
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("wp user reset-password: %s", result.Stderr)
	}
	rc.CacheInvalidate(site.Alias, []string{cache.CategoryUsers})
	fmt.Fprint(rc.Stdout, result.Stdout)
	return nil
}

func (c *UserExistsCmd) Run(g *Globals) error {
	return execBoolCheck(g, "user", "exists", c.User, "User")
}

func (c *UserMetaListCmd) Run(g *Globals) error {
	return execPassthrough(g, wpcli.New("user", "meta", "list").Arg(c.User))
}

func (c *UserMetaGetCmd) Run(g *Globals) error {
	return execPassthrough(g, wpcli.New("user", "meta", "get").Arg(c.User).Arg(c.Key))
}

func (c *UserMetaUpdateCmd) Run(g *Globals) error {
	return execValueWrite(g, valueWrite{
		cmd:    wpcli.New("user", "meta", "update"),
		script: userMetaUpdatePHP,
		call:   "update_user_meta",
		ids:    []string{"user", "meta key"},
		args:   []string{c.User, c.Key},
		value:  c.Value,
	})
}

func (c *UserMetaDeleteCmd) Run(g *Globals) error {
	return execPassthrough(g, wpcli.New("user", "meta", "delete").Arg(c.User).Arg(c.Key))
}
