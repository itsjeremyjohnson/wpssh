package cmd

import (
	"context"
	"fmt"

	"github.com/builtbyrobben/wpssh/internal/wpcli"
)

// Post commands

type PostCmd struct {
	List   PostListCmd   `cmd:"" help:"List posts"`
	Create PostCreateCmd `cmd:"" help:"Create a post"`
	Get    PostGetCmd    `cmd:"" help:"Get post details"`
	Update PostUpdateCmd `cmd:"" help:"Update a post"`
	Delete PostDeleteCmd `cmd:"" help:"Delete a post"`
	Exists PostExistsCmd `cmd:"" help:"Check if post exists"`
	Meta   PostMetaCmd   `cmd:"" help:"Post meta management"`
}

type PostListCmd struct {
	PostType string `help:"Post type" name:"post_type"`
}
type PostCreateCmd struct {
	PostTitle string `help:"Post title" name:"post_title"`
}
type PostGetCmd struct {
	ID int `arg:"" help:"Post ID"`
}
type PostUpdateCmd struct {
	ID int `arg:"" help:"Post ID"`
}
type PostDeleteCmd struct {
	ID    int  `arg:"" help:"Post ID"`
	Force bool `help:"Skip trash"`
}
type PostExistsCmd struct {
	ID int `arg:"" help:"Post ID"`
}
type PostMetaCmd struct {
	List   PostMetaListCmd   `cmd:"" help:"List post meta"`
	Get    PostMetaGetCmd    `cmd:"" help:"Get post meta"`
	Update PostMetaUpdateCmd `cmd:"" help:"Update post meta"`
	Delete PostMetaDeleteCmd `cmd:"" help:"Delete post meta"`
}
type PostMetaListCmd struct {
	ID int `arg:"" help:"Post ID"`
}
type PostMetaGetCmd struct {
	ID  int    `arg:"" help:"Post ID"`
	Key string `arg:"" help:"Meta key"`
}
type PostMetaUpdateCmd struct {
	ID    int    `arg:"" help:"Post ID"`
	Key   string `arg:"" help:"Meta key"`
	Value string `arg:"" help:"Meta value"`
}
type PostMetaDeleteCmd struct {
	ID  int    `arg:"" help:"Post ID"`
	Key string `arg:"" help:"Meta key"`
}

func (c *PostListCmd) Run(g *Globals) error {
	rc, err := NewRunContext(g)
	if err != nil {
		return err
	}
	defer rc.Close()
	site, err := rc.ResolveSite()
	if err != nil {
		return err
	}

	builder := wpcli.New("post", "list").Format("json")
	if c.PostType != "" {
		builder.Flag("post_type", c.PostType)
	}
	result, err := rc.ExecWP(context.Background(), site, builder.Build(site.WPPath))
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("wp post list: %s", result.Stderr)
	}
	posts, err := wpcli.ParseJSON[wpcli.Post](result.Stdout)
	if err != nil {
		return err
	}
	return rc.Formatter.Format(posts)
}

func (c *PostCreateCmd) Run(g *Globals) error {
	rc, err := NewRunContext(g)
	if err != nil {
		return err
	}
	defer rc.Close()
	site, err := rc.ResolveSite()
	if err != nil {
		return err
	}

	builder := wpcli.New("post", "create")
	if c.PostTitle != "" {
		builder.Flag("post_title", c.PostTitle)
	}
	result, err := rc.ExecWP(context.Background(), site, builder.Build(site.WPPath))
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("wp post create: %s", result.Stderr)
	}
	fmt.Fprint(rc.Stdout, result.Stdout)
	return nil
}

func (c *PostGetCmd) Run(g *Globals) error {
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
		wpcli.New("post", "get").Arg(fmt.Sprint(c.ID)).Format("json").Build(site.WPPath))
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("wp post get: %s", result.Stderr)
	}
	post, err := wpcli.ParseSingle[wpcli.Post](result.Stdout)
	if err != nil {
		return err
	}
	return rc.Formatter.Format(post)
}

func (c *PostUpdateCmd) Run(g *Globals) error {
	return execPassthrough(g, wpcli.New("post", "update"), fmt.Sprint(c.ID))
}

func (c *PostDeleteCmd) Run(g *Globals) error {
	rc, err := NewRunContext(g)
	if err != nil {
		return err
	}
	defer rc.Close()
	site, err := rc.ResolveSite()
	if err != nil {
		return err
	}

	builder := wpcli.New("post", "delete").Arg(fmt.Sprint(c.ID))
	if c.Force {
		builder.BoolFlag("force")
	}
	result, err := rc.ExecWP(context.Background(), site, builder.Build(site.WPPath))
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("wp post delete: %s", result.Stderr)
	}
	fmt.Fprint(rc.Stdout, result.Stdout)
	return nil
}

func (c *PostExistsCmd) Run(g *Globals) error {
	return execBoolCheck(g, "post", "exists", fmt.Sprint(c.ID), "Post")
}

func (c *PostMetaListCmd) Run(g *Globals) error {
	return execPassthrough(g, wpcli.New("post", "meta", "list"), fmt.Sprint(c.ID))
}

func (c *PostMetaGetCmd) Run(g *Globals) error {
	return execPassthrough(g, wpcli.New("post", "meta", "get"), fmt.Sprint(c.ID), c.Key)
}

func (c *PostMetaUpdateCmd) Run(g *Globals) error {
	return execValueWrite(g, valueWrite{
		cmd:    wpcli.New("post", "meta", "update"),
		script: postMetaUpdatePHP,
		call:   "update_post_meta",
		args:   []string{fmt.Sprint(c.ID), c.Key, c.Value},
	})
}

func (c *PostMetaDeleteCmd) Run(g *Globals) error {
	return execPassthrough(g, wpcli.New("post", "meta", "delete"), fmt.Sprint(c.ID), c.Key)
}
