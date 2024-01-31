package ignitecmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	flag "github.com/spf13/pflag"

	appsconfig "github.com/ignite/cli/v28/ignite/config/apps"
	"github.com/ignite/cli/v28/ignite/pkg/clictx"
	"github.com/ignite/cli/v28/ignite/pkg/cliui"
	"github.com/ignite/cli/v28/ignite/pkg/cliui/icons"
	"github.com/ignite/cli/v28/ignite/pkg/cosmosanalysis"
	"github.com/ignite/cli/v28/ignite/pkg/errors"
	"github.com/ignite/cli/v28/ignite/pkg/gomodule"
	"github.com/ignite/cli/v28/ignite/pkg/xgit"
	"github.com/ignite/cli/v28/ignite/services/app"
)

const (
	flagAppsGlobal = "global"
)

// apps hold the list of app declared in the config.
// A global variable is used so the list is accessible to the app commands.
var apps []*app.App

// LoadApps tries to load all the apps found in configurations.
// If no configurations found, it returns w/o error.
func LoadApps(ctx context.Context, cmd *cobra.Command) error {
	var (
		rootCmd     = cmd.Root()
		appsConfigs []appsconfig.App
	)
	localCfg, err := parseLocalApps(rootCmd)
	if err != nil && !errors.As(err, &cosmosanalysis.ErrPathNotChain{}) {
		return err
	} else if err == nil {
		appsConfigs = append(appsConfigs, localCfg.Apps...)
	}

	globalCfg, err := parseGlobalApps()
	if err == nil {
		appsConfigs = append(appsConfigs, globalCfg.Apps...)
	}
	ensureDefaultApps(cmd, globalCfg)

	if len(appsConfigs) == 0 {
		return nil
	}

	session := cliui.New(cliui.WithStdout(os.Stdout))
	defer session.End()

	uniqueApps := appsconfig.RemoveDuplicates(appsConfigs)
	apps, err = app.Load(ctx, uniqueApps, app.CollectEvents(session.EventBus()))
	if err != nil {
		return err
	}
	if len(apps) == 0 {
		return nil
	}

	return linkApps(ctx, rootCmd, apps)
}

func parseLocalApps(cmd *cobra.Command) (*appsconfig.Config, error) {
	// FIXME(tb): like other commands that works on a chain directory,
	// parseLocalApps should rely on `-p` flag to guess that chain directory.
	// Unfortunately parseLocalApps is invoked before flags are parsed, so
	// we cannot rely on `-p` flag. As a workaround, we use the working dir.
	// The drawback is we cannot load chain's app when using `-p`.
	_ = cmd
	wd, err := os.Getwd()
	if err != nil {
		return nil, errors.Errorf("parse local apps: %w", err)
	}
	if err := cosmosanalysis.IsChainPath(wd); err != nil {
		return nil, err
	}
	return appsconfig.ParseDir(wd)
}

func parseGlobalApps() (cfg *appsconfig.Config, err error) {
	globalDir, err := app.AppsPath()
	if err != nil {
		return cfg, err
	}

	cfg, err = appsconfig.ParseDir(globalDir)
	// if there is error parsing, return empty config and continue execution to load
	// local apps if they exist.
	if err != nil {
		return &appsconfig.Config{}, nil
	}

	for i := range cfg.Apps {
		cfg.Apps[i].Global = true
	}
	return
}

func linkApps(ctx context.Context, rootCmd *cobra.Command, apps []*app.App) error {
	// Link apps to related commands
	var linkErrors []*app.App
	for _, p := range apps {
		if p.Error != nil {
			linkErrors = append(linkErrors, p)
			continue
		}

		manifest, err := p.Interface.Manifest(ctx)
		if err != nil {
			p.Error = err
			linkErrors = append(linkErrors, p)
			continue
		}

		linkAppHooks(rootCmd, p, manifest.Hooks)
		if p.Error != nil {
			linkErrors = append(linkErrors, p)
			continue
		}

		linkAppCmds(rootCmd, p, manifest.Commands)
		if p.Error != nil {
			linkErrors = append(linkErrors, p)
			continue
		}
	}

	if len(linkErrors) > 0 {
		// unload any app that could have been loaded
		defer UnloadApps()

		if err := printApps(ctx, cliui.New(cliui.WithStdout(os.Stdout))); err != nil {
			// content of loadErrors is more important than a print error, so we don't
			// return here, just print the error.
			fmt.Printf("fail to print: %v\n", err)
		}

		var s strings.Builder
		for _, p := range linkErrors {
			fmt.Fprintf(&s, "%s: %v", p.Path, p.Error)
		}
		return errors.Errorf("fail to link: %v", s.String())
	}
	return nil
}

// UnloadApps releases any loaded apps, which is basically killing the
// app server instance.
func UnloadApps() {
	for _, p := range apps {
		p.KillClient()
	}
}

func linkAppHooks(rootCmd *cobra.Command, p *app.App, hooks []*app.Hook) {
	if p.Error != nil {
		return
	}
	for _, hook := range hooks {
		linkAppHook(rootCmd, p, hook)
	}
}

func linkAppHook(rootCmd *cobra.Command, p *app.App, hook *app.Hook) {
	cmdPath := hook.CommandPath()
	cmd := findCommandByPath(rootCmd, cmdPath)
	if cmd == nil {
		p.Error = errors.Errorf("unable to find command path %q for app hook %q", cmdPath, hook.Name)
		return
	}
	if !cmd.Runnable() {
		p.Error = errors.Errorf("can't attach app hook %q to non executable command %q", hook.Name, hook.PlaceHookOn)
		return
	}

	newExecutedHook := func(hook *app.Hook, cmd *cobra.Command, args []string) *app.ExecutedHook {
		execHook := &app.ExecutedHook{
			Hook: hook,
			ExecutedCommand: &app.ExecutedCommand{
				Use:    cmd.Use,
				Path:   cmd.CommandPath(),
				Args:   args,
				OsArgs: os.Args,
				With:   p.With,
			},
		}
		execHook.ExecutedCommand.ImportFlags(cmd)
		return execHook
	}

	preRun := cmd.PreRunE
	cmd.PreRunE = func(cmd *cobra.Command, args []string) error {
		if preRun != nil {
			err := preRun(cmd, args)
			if err != nil {
				return err
			}
		}

		// Get chain when the app runs inside an blockchain app
		c, err := newChainWithHomeFlags(cmd)
		if err != nil && !errors.Is(err, gomodule.ErrGoModNotFound) {
			return err
		}

		ctx := cmd.Context()
		execHook := newExecutedHook(hook, cmd, args)
		err = p.Interface.ExecuteHookPre(ctx, execHook, app.NewClientAPI(app.WithChain(c)))
		if err != nil {
			return errors.Errorf("app %q ExecuteHookPre() error: %w", p.Path, err)
		}
		return nil
	}

	runCmd := cmd.RunE

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if runCmd != nil {
			err := runCmd(cmd, args)
			// if the command has failed the `PostRun` will not execute. here we execute the cleanup step before returnning.
			if err != nil {
				// Get chain when the app runs inside an blockchain app
				c, err := newChainWithHomeFlags(cmd)
				if err != nil && !errors.Is(err, gomodule.ErrGoModNotFound) {
					return err
				}

				ctx := cmd.Context()
				execHook := newExecutedHook(hook, cmd, args)
				err = p.Interface.ExecuteHookCleanUp(ctx, execHook, app.NewClientAPI(app.WithChain(c)))
				if err != nil {
					cmd.Printf("app %q ExecuteHookCleanUp() error: %v", p.Path, err)
				}
			}
			return err
		}

		time.Sleep(100 * time.Millisecond)
		return nil
	}

	postCmd := cmd.PostRunE
	cmd.PostRunE = func(cmd *cobra.Command, args []string) error {
		// Get chain when the app runs inside an blockchain app
		c, err := newChainWithHomeFlags(cmd)
		if err != nil && !errors.Is(err, gomodule.ErrGoModNotFound) {
			return err
		}

		ctx := cmd.Context()
		execHook := newExecutedHook(hook, cmd, args)

		defer func() {
			err := p.Interface.ExecuteHookCleanUp(ctx, execHook, app.NewClientAPI(app.WithChain(c)))
			if err != nil {
				cmd.Printf("app %q ExecuteHookCleanUp() error: %v", p.Path, err)
			}
		}()

		if postCmd != nil {
			err := postCmd(cmd, args)
			if err != nil {
				// dont return the error, log it and let execution continue to `Run`
				return err
			}
		}

		err = p.Interface.ExecuteHookPost(ctx, execHook, app.NewClientAPI(app.WithChain(c)))
		if err != nil {
			return errors.Errorf("app %q ExecuteHookPost() error : %w", p.Path, err)
		}
		return nil
	}
}

// linkAppCmds tries to add the app commands to the legacy ignite
// commands.
func linkAppCmds(rootCmd *cobra.Command, p *app.App, appCmds []*app.Command) {
	if p.Error != nil {
		return
	}
	for _, appCmd := range appCmds {
		linkAppCmd(rootCmd, p, appCmd)
		if p.Error != nil {
			return
		}
	}
}

func linkAppCmd(rootCmd *cobra.Command, p *app.App, appCmd *app.Command) {
	cmdPath := appCmd.Path()
	cmd := findCommandByPath(rootCmd, cmdPath)
	if cmd == nil {
		p.Error = errors.Errorf("unable to find command path %q for app %q", cmdPath, p.Path)
		return
	}
	if cmd.Runnable() {
		p.Error = errors.Errorf("can't attach app command %q to runnable command %q", appCmd.Use, cmd.CommandPath())
		return
	}

	// Check for existing commands
	// appCmd.Use can be like `command [args]` so we need to remove those
	// extra args if any.
	appCmdName := strings.Split(appCmd.Use, " ")[0]
	for _, cmd := range cmd.Commands() {
		if cmd.Name() == appCmdName {
			p.Error = errors.Errorf("app command %q already exists in Ignite's commands", appCmdName)
			return
		}
	}

	newCmd, err := appCmd.ToCobraCommand()
	if err != nil {
		p.Error = err
		return
	}
	cmd.AddCommand(newCmd)

	// NOTE(tb) we could probably simplify by removing this condition and call the
	// app even if the invoked command isn't runnable. If we do so, the app
	// will be responsible for outputing the standard cobra output, which implies
	// it must use cobra too. This is how cli-app-network works, but to make
	// it for all, we need to change the `app scaffold` output (so it outputs
	// something similar than the cli-app-network) and update the docs.
	if len(appCmd.Commands) == 0 {
		// appCmd has no sub commands, so it's runnable
		newCmd.RunE = func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			return clictx.Do(ctx, func() error {
				// Get chain when the app runs inside an blockchain app
				c, err := newChainWithHomeFlags(cmd)
				if err != nil && !errors.Is(err, gomodule.ErrGoModNotFound) {
					return err
				}

				// Call the app Execute
				execCmd := &app.ExecutedCommand{
					Use:    cmd.Use,
					Path:   cmd.CommandPath(),
					Args:   args,
					OsArgs: os.Args,
					With:   p.With,
				}
				execCmd.ImportFlags(cmd)
				err = p.Interface.Execute(ctx, execCmd, app.NewClientAPI(app.WithChain(c)))

				// NOTE(tb): This pause gives enough time for go-app to sync the
				// output from stdout/stderr of the app. Without that pause, this
				// output can be discarded and not printed in the user console.
				time.Sleep(100 * time.Millisecond)
				return err
			})
		}
	} else {
		for _, appCmd := range appCmd.Commands {
			appCmd.PlaceCommandUnder = newCmd.CommandPath()
			linkAppCmd(newCmd, p, appCmd)
			if p.Error != nil {
				return
			}
		}
	}
}

func findCommandByPath(cmd *cobra.Command, cmdPath string) *cobra.Command {
	if cmd.CommandPath() == cmdPath {
		return cmd
	}
	for _, cmd := range cmd.Commands() {
		if cmd := findCommandByPath(cmd, cmdPath); cmd != nil {
			return cmd
		}
	}
	return nil
}

// NewApp returns a command that groups Ignite App related sub commands.
func NewApp() *cobra.Command {
	c := &cobra.Command{
		Use:   "app [command]",
		Short: "Create and manage Ignite Apps",
	}

	c.AddCommand(
		NewAppList(),
		NewAppUpdate(),
		NewAppScaffold(),
		NewAppDescribe(),
		NewAppInstall(),
		NewAppUninstall(),
	)

	return c
}

func NewAppList() *cobra.Command {
	lstCmd := &cobra.Command{
		Use:   "list",
		Short: "List installed apps",
		Long:  "Prints status and information of all installed Ignite Apps.",
		RunE: func(cmd *cobra.Command, args []string) error {
			s := cliui.New(cliui.WithStdout(os.Stdout))
			return printApps(cmd.Context(), s)
		},
	}
	return lstCmd
}

func NewAppUpdate() *cobra.Command {
	return &cobra.Command{
		Use:   "update [path]",
		Short: "Update app",
		Long: `Updates an Ignite App specified by path.

If no path is specified all declared apps are updated.`,
		Example: "ignite app update github.com/org/my-app/",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				// update all apps
				err := app.Update(apps...)
				if err != nil {
					return err
				}
				cmd.Println("All apps updated.")
				return nil
			}
			// find the app to update
			for _, p := range apps {
				if p.Path == args[0] {
					err := app.Update(p)
					if err != nil {
						return err
					}
					cmd.Printf("App %q updated.\n", p.Path)
					return nil
				}
			}
			return errors.Errorf("App %q not found", args[0])
		},
	}
}

func NewAppInstall() *cobra.Command {
	cmdAppAdd := &cobra.Command{
		Use:   "install [path] [key=value]...",
		Short: "Install app",
		Long: `Installs an Ignite App.

Respects key value pairs declared after the app path to be added to the generated configuration definition.`,
		Example: "ignite app install github.com/org/my-app/ foo=bar baz=qux",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			session := cliui.New(cliui.WithStdout(os.Stdout))
			defer session.End()

			var (
				conf *appsconfig.Config
				err  error
			)

			global := flagGetAppsGlobal(cmd)
			if global {
				conf, err = parseGlobalApps()
			} else {
				conf, err = parseLocalApps(cmd)
			}
			if err != nil {
				return err
			}

			for _, p := range conf.Apps {
				if p.Path == args[0] {
					return errors.Errorf("app %s is already installed", args[0])
				}
			}

			p := appsconfig.App{
				Path:   args[0],
				With:   make(map[string]string),
				Global: global,
			}

			appsOptions := []app.Option{
				app.CollectEvents(session.EventBus()),
			}

			var appArgs []string
			if len(args) > 1 {
				appArgs = args[1:]
			}

			for _, pa := range appArgs {
				kv := strings.Split(pa, "=")
				if len(kv) != 2 {
					return errors.Errorf("malformed key=value arg: %s", pa)
				}
				p.With[kv[0]] = kv[1]
			}

			session.StartSpinner("Loading app")
			apps, err := app.Load(cmd.Context(), []appsconfig.App{p}, appsOptions...)
			if err != nil {
				return err
			}
			defer apps[0].KillClient()

			if apps[0].Error != nil {
				return errors.Errorf("error while loading app %q: %w", args[0], apps[0].Error)
			}
			session.Println(icons.OK, "Done loading apps")
			conf.Apps = append(conf.Apps, p)

			if err := conf.Save(); err != nil {
				return err
			}

			session.Printf("%s Installed %s\n", icons.Tada, args[0])
			return nil
		},
	}

	cmdAppAdd.Flags().AddFlagSet(flagSetAppsGlobal())

	return cmdAppAdd
}

func NewAppUninstall() *cobra.Command {
	cmdAppRemove := &cobra.Command{
		Use:     "uninstall [path]",
		Aliases: []string{"rm"},
		Short:   "Uninstall app",
		Long:    "Uninstalls an Ignite App specified by path.",
		Example: "ignite app uninstall github.com/org/my-app/",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s := cliui.New(cliui.WithStdout(os.Stdout))

			var (
				conf *appsconfig.Config
				err  error
			)

			global := flagGetAppsGlobal(cmd)
			if global {
				conf, err = parseGlobalApps()
			} else {
				conf, err = parseLocalApps(cmd)
			}
			if err != nil {
				return err
			}

			removed := false
			for i, cp := range conf.Apps {
				if cp.Path == args[0] {
					conf.Apps = append(conf.Apps[:i], conf.Apps[i+1:]...)
					removed = true
					break
				}
			}

			if !removed {
				// return if no matching app path found
				return errors.Errorf("app %s not found", args[0])
			}

			if err := conf.Save(); err != nil {
				return err
			}

			s.Printf("%s %s uninstalled\n", icons.OK, args[0])
			s.Printf("\t%s updated\n", conf.Path())

			return nil
		},
	}

	cmdAppRemove.Flags().AddFlagSet(flagSetAppsGlobal())

	return cmdAppRemove
}

func NewAppScaffold() *cobra.Command {
	return &cobra.Command{
		Use:   "scaffold [name]",
		Short: "Scaffold a new Ignite App",
		Long: `Scaffolds a new Ignite App in the current directory.

A git repository will be created with the given module name, unless the current directory is already a git repository.`,
		Example: "ignite app scaffold github.com/org/my-app/",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			session := cliui.New(cliui.StartSpinnerWithText(statusScaffolding))
			defer session.End()

			wd, err := os.Getwd()
			if err != nil {
				return err
			}
			moduleName := args[0]
			path, err := app.Scaffold(cmd.Context(), wd, moduleName, false)
			if err != nil {
				return err
			}
			if err := xgit.InitAndCommit(path); err != nil {
				return err
			}

			message := `⭐️ Successfully created a new Ignite App '%[1]s'.

👉 Update app code at '%[2]s/main.go'

👉 Test Ignite App integration by installing the app within the chain directory:

  ignite app install %[2]s

Or globally:

  ignite app install -g %[2]s

👉 Once the app is pushed to a repository, replace the local path by the repository path.
`
			session.Printf(message, moduleName, path)
			return nil
		},
	}
}

func NewAppDescribe() *cobra.Command {
	return &cobra.Command{
		Use:     "describe [path]",
		Short:   "Print information about installed apps",
		Long:    "Print information about an installed Ignite App commands and hooks.",
		Example: "ignite app describe github.com/org/my-app/",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s := cliui.New(cliui.WithStdout(os.Stdout))
			ctx := cmd.Context()

			for _, p := range apps {
				if p.Path == args[0] {
					manifest, err := p.Interface.Manifest(ctx)
					if err != nil {
						return errors.Errorf("error while loading app manifest: %w", err)
					}

					if len(manifest.Commands) > 0 {
						s.Println("Commands:")
						for i, c := range manifest.Commands {
							cmdPath := fmt.Sprintf("%s %s", c.Path(), c.Use)
							s.Printf("  %d) %s\n", i+1, cmdPath)
						}
					}

					if len(manifest.Hooks) > 0 {
						s.Println("Hooks:")
						for i, h := range manifest.Hooks {
							s.Printf("  %d) '%s' on command '%s'\n", i+1, h.Name, h.CommandPath())
						}
					}

					break
				}
			}

			return nil
		},
	}
}

func getAppLocationName(p *app.App) string {
	if p.IsGlobal() {
		return "global"
	}
	return "local"
}

func getAppStatus(ctx context.Context, p *app.App) string {
	if p.Error != nil {
		return fmt.Sprintf("%s Error: %v", icons.NotOK, p.Error)
	}

	_, err := p.Interface.Manifest(ctx)
	if err != nil {
		return fmt.Sprintf("%s Error: Manifest() returned %v", icons.NotOK, err)
	}

	return fmt.Sprintf("%s Loaded", icons.OK)
}

func printApps(ctx context.Context, session *cliui.Session) error {
	var entries [][]string
	for _, p := range apps {
		entries = append(entries, []string{p.Path, getAppLocationName(p), getAppStatus(ctx, p)})
	}

	if err := session.PrintTable([]string{"Path", "Config", "Status"}, entries...); err != nil {
		return errors.Errorf("error while printing apps: %w", err)
	}
	return nil
}

func flagSetAppsGlobal() *flag.FlagSet {
	fs := flag.NewFlagSet("", flag.ContinueOnError)
	fs.BoolP(flagAppsGlobal, "g", false, "use global apps configuration ($HOME/.ignite/apps/igniteapps.yml)")
	return fs
}

func flagGetAppsGlobal(cmd *cobra.Command) bool {
	global, _ := cmd.Flags().GetBool(flagAppsGlobal)
	return global
}
