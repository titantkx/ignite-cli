package ignitecmd

import (
	"github.com/spf13/cobra"

	appsconfig "github.com/ignite/cli/v28/ignite/config/apps"
	"github.com/ignite/cli/v28/ignite/pkg/cliui"
	"github.com/ignite/cli/v28/ignite/services/app"
)

type defaultApp struct {
	use     string
	short   string
	aliases []string
	path    string
}

const (
	AppNetworkVersion = "v0.2.1"
	AppNetworkPath    = "github.com/ignite/cli-app-network@" + AppNetworkVersion
)

// defaultApps holds the app that are considered trustable and for which
// a command will added if the app is not already installed.
// When the user executes that command, the app is automatically installed.
var defaultApps = []defaultApp{
	{
		use:     "network",
		short:   "Launch a blockchain in production",
		aliases: []string{"n"},
		path:    AppNetworkPath,
	},
}

// ensureDefaultApps ensures that all defaultApps are wether registered
// in cfg OR have an install command added to rootCmd.
func ensureDefaultApps(rootCmd *cobra.Command, cfg *appsconfig.Config) {
	for _, dp := range defaultApps {
		// Check if app is declared in global config
		if cfg.HasApp(dp.path) {
			// app found nothing to do
			continue
		}
		// app not found in config, add a proxy install command
		rootCmd.AddCommand(newAppInstallCmd(dp))
	}
}

// newAppInstallCmd mimics the app command but acts as proxy to first:
// - register the config in the global config
// - load the app
// - execute the command thanks to the loaded app.
func newAppInstallCmd(dp defaultApp) *cobra.Command {
	return &cobra.Command{
		Use:                dp.use,
		Short:              dp.short,
		Aliases:            dp.aliases,
		DisableFlagParsing: true, // Avoid -h to skip command run
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := parseGlobalApps()
			if err != nil {
				return err
			}

			// add app to config
			appCfg := appsconfig.App{
				Path: dp.path,
			}
			cfg.Apps = append(cfg.Apps, appCfg)
			if err := cfg.Save(); err != nil {
				return err
			}

			session := cliui.New()
			defer session.End()

			// load and link the app
			apps, err := app.Load(
				cmd.Context(),
				[]appsconfig.App{appCfg},
				app.CollectEvents(session.EventBus()),
			)
			if err != nil {
				return err
			}
			defer apps[0].KillClient()

			// Keep reference of the root command before removal
			rootCmd := cmd.Root()
			// Remove this command before call to linkApps because a app is
			// usually not allowed to override an existing command.
			rootCmd.RemoveCommand(cmd)
			if err := linkApps(cmd.Context(), rootCmd, apps); err != nil {
				return err
			}
			// Execute the command
			return rootCmd.Execute()
		},
	}
}
