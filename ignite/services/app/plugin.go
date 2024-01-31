// Package app implements ignite app management.
// An ignite app is a binary which communicates with the ignite binary
// via RPC thanks to the github.com/hashicorp/go-plugin library.
package app

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/hashicorp/go-hclog"
	hplugin "github.com/hashicorp/go-plugin"

	"github.com/ignite/cli/v28/ignite/config"
	appsconfig "github.com/ignite/cli/v28/ignite/config/apps"
	"github.com/ignite/cli/v28/ignite/pkg/cliui/icons"
	"github.com/ignite/cli/v28/ignite/pkg/env"
	"github.com/ignite/cli/v28/ignite/pkg/errors"
	"github.com/ignite/cli/v28/ignite/pkg/events"
	"github.com/ignite/cli/v28/ignite/pkg/gocmd"
	"github.com/ignite/cli/v28/ignite/pkg/xfilepath"
	"github.com/ignite/cli/v28/ignite/pkg/xgit"
	"github.com/ignite/cli/v28/ignite/pkg/xurl"
)

// AppsPath holds the app cache directory.
var AppsPath = xfilepath.Mkdir(xfilepath.Join(
	config.DirPath,
	xfilepath.Path("apps"),
))

// App represents a ignite app.
type App struct {
	// Embed the app configuration.
	appsconfig.App

	// Interface allows to communicate with the app via RPC.
	Interface Interface

	// If any error occurred during the app load, it's stored here.
	Error error

	name      string
	repoPath  string
	cloneURL  string
	cloneDir  string
	reference string
	srcPath   string

	client *hplugin.Client

	// Holds a cache of the app manifest to prevent mant calls over the rpc boundary.
	manifest *Manifest

	// If a app's ShareHost flag is set to true, isHost is used to discern if a
	// app instance is controlling the rpc server.
	isHost       bool
	isSharedHost bool

	ev events.Bus

	stdout io.Writer
	stderr io.Writer
}

// Option configures App.
type Option func(*App)

// CollectEvents collects events from the chain.
func CollectEvents(ev events.Bus) Option {
	return func(p *App) {
		p.ev = ev
	}
}

func RedirectStdout(w io.Writer) Option {
	return func(p *App) {
		p.stdout = w
		p.stderr = w
	}
}

// Load loads the apps found in the chain config.
//
// There's 2 kinds of apps, local or remote.
// Local apps have their path starting with a `/`, while remote apps don't.
// Local apps are useful for development purpose.
// Remote apps require to be fetched first, in $HOME/.ignite/apps folder,
// then they are loaded from there.
//
// If an error occurs during a app load, it's not returned but rather stored in
// the `App.Error` field. This prevents the loading of other apps to be interrupted.
func Load(ctx context.Context, apps []appsconfig.App, options ...Option) ([]*App, error) {
	appsDir, err := AppsPath()
	if err != nil {
		return nil, errors.WithStack(err)
	}
	var loaded []*App
	for _, cp := range apps {
		p := newApp(appsDir, cp, options...)
		p.load(ctx)

		loaded = append(loaded, p)
	}
	return loaded, nil
}

// Update removes the cache directory of apps and fetch them again.
func Update(apps ...*App) error {
	for _, p := range apps {
		if err := p.clean(); err != nil {
			return err
		}
		p.fetch()
	}
	return nil
}

// newApp creates a App from configuration.
func newApp(appsDir string, cp appsconfig.App, options ...Option) *App {
	var (
		p = &App{
			App:    cp,
			stdout: os.Stdout,
			stderr: os.Stderr,
		}
		appPath = cp.Path
	)
	if appPath == "" {
		p.Error = errors.Errorf(`missing app property "path"`)
		return p
	}

	// Apply the options
	for _, apply := range options {
		apply(p)
	}

	if strings.HasPrefix(appPath, "/") {
		// This is a local app, check if the file exists
		st, err := os.Stat(appPath)
		if err != nil {
			p.Error = errors.Wrapf(err, "local app path %q not found", appPath)
			return p
		}
		if !st.IsDir() {
			p.Error = errors.Errorf("local app path %q is not a directory", appPath)
			return p
		}
		p.srcPath = appPath
		p.name = path.Base(appPath)
		return p
	}
	// This is a remote app, parse the URL
	if i := strings.LastIndex(appPath, "@"); i != -1 {
		// path contains a reference
		p.reference = appPath[i+1:]
		appPath = appPath[:i]
	}
	parts := strings.Split(appPath, "/")
	if len(parts) < 3 {
		p.Error = errors.Errorf("app path %q is not a valid repository URL", appPath)
		return p
	}
	p.repoPath = path.Join(parts[:3]...)
	p.cloneURL, _ = xurl.HTTPS(p.repoPath)

	if len(p.reference) > 0 {
		ref := strings.ReplaceAll(p.reference, "/", "-")
		p.cloneDir = path.Join(appsDir, fmt.Sprintf("%s-%s", p.repoPath, ref))
		p.repoPath += "@" + p.reference
	} else {
		p.cloneDir = path.Join(appsDir, p.repoPath)
	}

	// App can have a subpath within its repository.
	// For example, "github.com/ignite/apps/app1" where "app1" is the subpath.
	repoSubPath := path.Join(parts[3:]...)

	p.srcPath = path.Join(p.cloneDir, repoSubPath)
	p.name = path.Base(appPath)

	return p
}

// KillClient kills the running app client.
func (p *App) KillClient() {
	if p.isSharedHost && !p.isHost {
		// Don't send kill signal to a shared-host app when this process isn't
		// the one who initiated it.
		return
	}

	if p.client != nil {
		p.client.Kill()
	}

	if p.isHost {
		_ = deleteConfCache(p.Path)
		p.isHost = false
	}
}

// Manifest returns app's manigest.
// The manifest is available after the app has been loaded.
func (p App) Manifest() *Manifest {
	return p.manifest
}

func (p App) binaryName() string {
	return fmt.Sprintf("%s.ign", p.name)
}

func (p App) binaryPath() string {
	return path.Join(p.srcPath, p.binaryName())
}

// load tries to fill p.Interface, ensuring the app is usable.
func (p *App) load(ctx context.Context) {
	if p.Error != nil {
		return
	}
	_, err := os.Stat(p.srcPath)
	if err != nil {
		// srcPath found, need to fetch the app
		p.fetch()
		if p.Error != nil {
			return
		}
	}

	if p.IsLocalPath() {
		// trigger rebuild for local app if binary is outdated
		if p.outdatedBinary() {
			p.build(ctx)
		}
	} else {
		// Check if binary is already build
		_, err = os.Stat(p.binaryPath())
		if err != nil {
			// binary not found, need to build it
			p.build(ctx)
		}
	}
	if p.Error != nil {
		return
	}
	// appMap is the map of apps we can dispense.
	appMap := map[string]hplugin.Plugin{
		p.name: NewGRPC(nil),
	}
	// Create an hclog.Logger
	logLevel := hclog.Error
	if env.DebugEnabled() {
		logLevel = hclog.Trace
	}
	logger := hclog.New(&hclog.LoggerOptions{
		Name:   fmt.Sprintf("app %s", p.Path),
		Output: os.Stderr,
		Level:  logLevel,
	})

	// Common app client configuration values
	cfg := &hplugin.ClientConfig{
		HandshakeConfig:  HandshakeConfig(),
		Plugins:          appMap,
		Logger:           logger,
		SyncStderr:       p.stdout,
		SyncStdout:       p.stderr,
		AllowedProtocols: []hplugin.Protocol{hplugin.ProtocolGRPC},
	}

	if checkConfCache(p.Path) {
		rconf, err := readConfigCache(p.Path)
		if err != nil {
			p.Error = err
			return
		}

		// Attach to an existing app process
		cfg.Reattach = &rconf
		p.client = hplugin.NewClient(cfg)
	} else {
		// Launch a new app process
		cfg.Cmd = exec.Command(p.binaryPath())
		p.client = hplugin.NewClient(cfg)
	}

	// Connect via gRPC
	rpcClient, err := p.client.Client()
	if err != nil {
		p.Error = errors.Wrapf(err, "connecting")
		return
	}

	// Request the app
	raw, err := rpcClient.Dispense(p.name)
	if err != nil {
		p.Error = errors.Wrapf(err, "dispensing")
		return
	}

	// We should have an Interface now! This feels like a normal interface
	// implementation but is in fact over an gRPC connection.
	p.Interface = raw.(Interface)

	m, err := p.Interface.Manifest(ctx)
	if err != nil {
		p.Error = errors.Wrapf(err, "manifest load")
		return
	}

	p.isSharedHost = m.SharedHost

	// Cache the manifest to avoid extra app requests
	p.manifest = m

	// write the rpc context to cache if the app is declared as host.
	// writing it to cache as lost operation within load to assure rpc client's reattach config
	// is hydrated.
	if m.SharedHost && !checkConfCache(p.Path) {
		err := writeConfigCache(p.Path, *p.client.ReattachConfig())
		if err != nil {
			p.Error = err
			return
		}

		// set the app's rpc server as host so other app clients may share
		p.isHost = true
	}
}

// fetch clones the app repository at the expected reference.
func (p *App) fetch() {
	if p.IsLocalPath() {
		return
	}
	if p.Error != nil {
		return
	}
	p.ev.Send(fmt.Sprintf("Fetching app %q", p.cloneURL), events.ProgressStart())
	defer p.ev.Send(fmt.Sprintf("%s App fetched %q", icons.OK, p.cloneURL), events.ProgressFinish())

	urlref := strings.Join([]string{p.cloneURL, p.reference}, "@")
	err := xgit.Clone(context.Background(), urlref, p.cloneDir)
	if err != nil {
		p.Error = errors.Wrapf(err, "cloning %q", p.repoPath)
	}
}

// build compiles the app binary.
func (p *App) build(ctx context.Context) {
	if p.Error != nil {
		return
	}
	p.ev.Send(fmt.Sprintf("Building app %q", p.Path), events.ProgressStart())
	defer p.ev.Send(fmt.Sprintf("%s App built %q", icons.OK, p.Path), events.ProgressFinish())

	if err := gocmd.ModTidy(ctx, p.srcPath); err != nil {
		p.Error = errors.Wrapf(err, "go mod tidy")
		return
	}
	if err := gocmd.Build(ctx, p.binaryName(), p.srcPath, nil); err != nil {
		p.Error = errors.Wrapf(err, "go build")
		return
	}
}

// clean removes the app cache (only for remote apps).
func (p *App) clean() error {
	if p.Error != nil {
		// Dont try to clean apps with error
		return nil
	}
	if p.IsLocalPath() {
		// Not a remote app, nothing to clean
		return nil
	}
	// Clean the cloneDir, next time the ignite command will be invoked, the
	// app will be fetched again.
	err := os.RemoveAll(p.cloneDir)
	return errors.WithStack(err)
}

// outdatedBinary returns true if the app binary is older than the other
// files in p.srcPath.
// Also returns true if the app binary is absent.
func (p *App) outdatedBinary() bool {
	var (
		binaryTime time.Time
		mostRecent time.Time
	)
	err := filepath.Walk(p.srcPath, func(path string, info fs.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if path == p.binaryPath() {
			binaryTime = info.ModTime()
			return nil
		}
		t := info.ModTime()
		if mostRecent.IsZero() || t.After(mostRecent) {
			mostRecent = t
		}
		return nil
	})
	if err != nil {
		fmt.Printf("error while walking app source path %q\n", p.srcPath)
		return false
	}
	return mostRecent.After(binaryTime)
}
