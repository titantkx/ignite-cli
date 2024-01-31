package main

import (
	"context"
	"fmt"

	hplugin "github.com/hashicorp/go-plugin"
)

type app struct{}

func (app) Manifest(ctx context.Context) (*app.Manifest, error) {
	return &app.Manifest{
		Name: "execute_ok",
	}, nil
}

func (app) Execute(ctx context.Context, cmd *app.ExecutedCommand, api app.ClientAPI) error {
	c, _ := api.GetChainInfo(ctx)
	fmt.Printf(
		"ok args=%s chainid=%s appPath=%s configPath=%s home=%s rpcAddress=%s\n",
		cmd.Args, c.ChainId, c.AppPath, c.ConfigPath, c.Home, c.RpcAddress,
	)
	return nil
}

func (app) ExecuteHookPre(ctx context.Context, h *app.ExecutedHook, api app.ClientAPI) error {
	return nil
}

func (app) ExecuteHookPost(ctx context.Context, h *app.ExecutedHook, api app.ClientAPI) error {
	return nil
}

func (app) ExecuteHookCleanUp(ctx context.Context, h *app.ExecutedHook, api app.ClientAPI) error {
	return nil
}

func main() {
	hplugin.Serve(&hplugin.ServeConfig{
		HandshakeConfig: app.HandshakeConfig(),
		Plugins: map[string]hplugin.Plugin{
			"execute_ok": app.NewGRPC(&app{}),
		},
		GRPCServer: hplugin.DefaultGRPCServer,
	})
}
