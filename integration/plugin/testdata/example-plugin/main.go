package main

import (
	"context"
	"fmt"

	hplugin "github.com/hashicorp/go-plugin"

	"github.com/ignite/cli/v28/ignite/services/app"
)

type p struct{}

func (p) Manifest(context.Context) (*app.Manifest, error) {
	return &app.Manifest{
		Name: "example-plugin",
		Commands: []*app.Command{
			{
				Use:   "example-plugin",
				Short: "Explain what the command is doing...",
				Long:  "Long description goes here...",
				Flags: []*app.Flag{
					{Name: "my-flag", Type: app.FlagTypeString, Usage: "my flag description"},
				},
				PlaceCommandUnder: "ignite",
			},
		},
		Hooks: []*app.Hook{},
	}, nil
}

func (p) Execute(ctx context.Context, cmd *app.ExecutedCommand, api app.ClientAPI) error {
	fmt.Printf("Hello I'm the example-plugin plugin\n")
	fmt.Printf("My executed command: %q\n", cmd.Path)
	fmt.Printf("My args: %v\n", cmd.Args)

	flags, err := cmd.NewFlags()
	if err != nil {
		return err
	}

	myFlag, _ := flags.GetString("my-flag")
	fmt.Printf("My flags: my-flag=%q\n", myFlag)
	fmt.Printf("My config parameters: %v\n", cmd.With)

	fmt.Println(api.GetChainInfo(ctx))

	return nil
}

func (p) ExecuteHookPre(_ context.Context, h *app.ExecutedHook, _ app.ClientAPI) error {
	fmt.Printf("Executing hook pre %q\n", h.Hook.GetName())
	return nil
}

func (p) ExecuteHookPost(_ context.Context, h *app.ExecutedHook, _ app.ClientAPI) error {
	fmt.Printf("Executing hook post %q\n", h.Hook.GetName())
	return nil
}

func (p) ExecuteHookCleanUp(_ context.Context, h *app.ExecutedHook, _ app.ClientAPI) error {
	fmt.Printf("Executing hook cleanup %q\n", h.Hook.GetName())
	return nil
}

func main() {
	hplugin.Serve(&hplugin.ServeConfig{
		HandshakeConfig: app.HandshakeConfig(),
		Plugins: map[string]hplugin.Plugin{
			"example-plugin": app.NewGRPC(&p{}),
		},
		GRPCServer: hplugin.DefaultGRPCServer,
	})
}
