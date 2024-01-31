package main

import (
	"context"
	"errors"

	hplugin "github.com/hashicorp/go-plugin"
)

type app struct{}

func (app) Manifest(ctx context.Context) (*app.Manifest, error) {
	return &app.Manifest{
		Name: "execute_fail",
	}, nil
}

func (app) Execute(ctx context.Context, cmd *app.ExecutedCommand, api app.ClientAPI) error {
	return errors.New("fail")
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
			"execute_fail": app.NewGRPC(&app{}),
		},
		GRPCServer: hplugin.DefaultGRPCServer,
	})
}
