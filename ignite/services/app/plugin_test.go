package app

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	hplugin "github.com/hashicorp/go-plugin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	appsconfig "github.com/ignite/cli/v28/ignite/config/apps"
	"github.com/ignite/cli/v28/ignite/pkg/errors"
	"github.com/ignite/cli/v28/ignite/pkg/gocmd"
	"github.com/ignite/cli/v28/ignite/pkg/gomodule"
)

func TestNewApp(t *testing.T) {
	wd, err := os.Getwd()
	require.NoError(t, err)

	tests := []struct {
		name        string
		appCfg      appsconfig.App
		expectedApp App
	}{
		{
			name: "fail: empty path",
			expectedApp: App{
				Error:  errors.Errorf(`missing app property "path"`),
				stdout: os.Stdout,
				stderr: os.Stderr,
			},
		},
		{
			name:   "fail: local app doesnt exists",
			appCfg: appsconfig.App{Path: "/xxx/yyy/app"},
			expectedApp: App{
				Error:  errors.Errorf(`local app path "/xxx/yyy/app" not found`),
				stdout: os.Stdout,
				stderr: os.Stderr,
			},
		},
		{
			name:   "fail: local app is not a directory",
			appCfg: appsconfig.App{Path: path.Join(wd, "testdata/fakebin")},
			expectedApp: App{
				Error:  errors.Errorf(fmt.Sprintf("local app path %q is not a directory", path.Join(wd, "testdata/fakebin"))),
				stdout: os.Stdout,
				stderr: os.Stderr,
			},
		},
		{
			name:   "ok: local app",
			appCfg: appsconfig.App{Path: path.Join(wd, "testdata")},
			expectedApp: App{
				srcPath: path.Join(wd, "testdata"),
				name:    "testdata",
				stdout:  os.Stdout,
				stderr:  os.Stderr,
			},
		},
		{
			name:   "fail: remote app with only domain",
			appCfg: appsconfig.App{Path: "github.com"},
			expectedApp: App{
				Error:  errors.Errorf(`app path "github.com" is not a valid repository URL`),
				stdout: os.Stdout,
				stderr: os.Stderr,
			},
		},
		{
			name:   "fail: remote app with incomplete URL",
			appCfg: appsconfig.App{Path: "github.com/ignite"},
			expectedApp: App{
				Error:  errors.Errorf(`app path "github.com/ignite" is not a valid repository URL`),
				stdout: os.Stdout,
				stderr: os.Stderr,
			},
		},
		{
			name:   "ok: remote app",
			appCfg: appsconfig.App{Path: "github.com/ignite/app"},
			expectedApp: App{
				repoPath:  "github.com/ignite/app",
				cloneURL:  "https://github.com/ignite/app",
				cloneDir:  ".ignite/apps/github.com/ignite/app",
				reference: "",
				srcPath:   ".ignite/apps/github.com/ignite/app",
				name:      "app",
				stdout:    os.Stdout,
				stderr:    os.Stderr,
			},
		},
		{
			name:   "ok: remote app with @ref",
			appCfg: appsconfig.App{Path: "github.com/ignite/app@develop"},
			expectedApp: App{
				repoPath:  "github.com/ignite/app@develop",
				cloneURL:  "https://github.com/ignite/app",
				cloneDir:  ".ignite/apps/github.com/ignite/app-develop",
				reference: "develop",
				srcPath:   ".ignite/apps/github.com/ignite/app-develop",
				name:      "app",
				stdout:    os.Stdout,
				stderr:    os.Stderr,
			},
		},
		{
			name:   "ok: remote app with @ref containing slash",
			appCfg: appsconfig.App{Path: "github.com/ignite/app@package/v1.0.0"},
			expectedApp: App{
				repoPath:  "github.com/ignite/app@package/v1.0.0",
				cloneURL:  "https://github.com/ignite/app",
				cloneDir:  ".ignite/apps/github.com/ignite/app-package-v1.0.0",
				reference: "package/v1.0.0",
				srcPath:   ".ignite/apps/github.com/ignite/app-package-v1.0.0",
				name:      "app",
				stdout:    os.Stdout,
				stderr:    os.Stderr,
			},
		},
		{
			name:   "ok: remote app with subpath",
			appCfg: appsconfig.App{Path: "github.com/ignite/app/app1"},
			expectedApp: App{
				repoPath:  "github.com/ignite/app",
				cloneURL:  "https://github.com/ignite/app",
				cloneDir:  ".ignite/apps/github.com/ignite/app",
				reference: "",
				srcPath:   ".ignite/apps/github.com/ignite/app/app1",
				name:      "app1",
				stdout:    os.Stdout,
				stderr:    os.Stderr,
			},
		},
		{
			name:   "ok: remote app with subpath and @ref",
			appCfg: appsconfig.App{Path: "github.com/ignite/app/app1@develop"},
			expectedApp: App{
				repoPath:  "github.com/ignite/app@develop",
				cloneURL:  "https://github.com/ignite/app",
				cloneDir:  ".ignite/apps/github.com/ignite/app-develop",
				reference: "develop",
				srcPath:   ".ignite/apps/github.com/ignite/app-develop/app1",
				name:      "app1",
				stdout:    os.Stdout,
				stderr:    os.Stderr,
			},
		},
		{
			name:   "ok: remote app with subpath and @ref containing slash",
			appCfg: appsconfig.App{Path: "github.com/ignite/app/app1@package/v1.0.0"},
			expectedApp: App{
				repoPath:  "github.com/ignite/app@package/v1.0.0",
				cloneURL:  "https://github.com/ignite/app",
				cloneDir:  ".ignite/apps/github.com/ignite/app-package-v1.0.0",
				reference: "package/v1.0.0",
				srcPath:   ".ignite/apps/github.com/ignite/app-package-v1.0.0/app1",
				name:      "app1",
				stdout:    os.Stdout,
				stderr:    os.Stderr,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.expectedApp.App = tt.appCfg

			p := newApp(".ignite/apps", tt.appCfg)

			assertApp(t, tt.expectedApp, *p)
		})
	}
}

// Helper to make a local git repository with gofile committed.
// Returns the repo directory and the git.Repository
func makeGitRepo(t *testing.T, name string) (string, *git.Repository) {
	t.Helper()

	require := require.New(t)
	repoDir := t.TempDir()
	scaffoldApp(t, repoDir, "github.com/ignite/"+name, false)

	repo, err := git.PlainInit(repoDir, false)
	require.NoError(err)

	w, err := repo.Worktree()
	require.NoError(err)

	_, err = w.Add(".")
	require.NoError(err)

	_, err = w.Commit("msg", &git.CommitOptions{
		Author: &object.Signature{
			Name:  "bob",
			Email: "bob@example.com",
			When:  time.Now(),
		},
	})
	require.NoError(err)
	return repoDir, repo
}

type TestClientAPI struct{ ClientAPI }

func (TestClientAPI) GetChainInfo(context.Context) (*ChainInfo, error) {
	return &ChainInfo{}, nil
}

func TestAppLoad(t *testing.T) {
	wd, err := os.Getwd()
	require.NoError(t, err)

	clientAPI := &TestClientAPI{}

	tests := []struct {
		name          string
		buildApp      func(t *testing.T) App
		expectedError string
	}{
		{
			name: "fail: app is already in error",
			buildApp: func(t *testing.T) App {
				return App{
					Error: errors.New("oups"),
				}
			},
			expectedError: `oups`,
		},
		{
			name: "fail: no go files in srcPath",
			buildApp: func(t *testing.T) App {
				return App{
					srcPath: path.Join(wd, "testdata"),
					name:    "testdata",
				}
			},
			expectedError: `no Go files in`,
		},
		{
			name: "ok: from local",
			buildApp: func(t *testing.T) App {
				path := scaffoldApp(t, t.TempDir(), "github.com/foo/bar", false)
				return App{
					srcPath: path,
					name:    "bar",
				}
			},
		},
		{
			name: "ok: from git repo",
			buildApp: func(t *testing.T) App {
				repoDir, _ := makeGitRepo(t, "remote")
				cloneDir := t.TempDir()

				return App{
					cloneURL: repoDir,
					cloneDir: cloneDir,
					srcPath:  path.Join(cloneDir, "remote"),
					name:     "remote",
				}
			},
		},
		{
			name: "fail: git repo doesnt exists",
			buildApp: func(t *testing.T) App {
				cloneDir := t.TempDir()

				return App{
					repoPath: "/xxxx/yyyy",
					cloneURL: "/xxxx/yyyy",
					cloneDir: cloneDir,
					srcPath:  path.Join(cloneDir, "app"),
				}
			},
			expectedError: `cloning "/xxxx/yyyy": repository not found`,
		},
		{
			name: "ok: from git repo with tag",
			buildApp: func(t *testing.T) App {
				repoDir, repo := makeGitRepo(t, "remote-tag")
				h, err := repo.Head()
				require.NoError(t, err)
				_, err = repo.CreateTag("v1", h.Hash(), &git.CreateTagOptions{
					Tagger:  &object.Signature{Name: "me"},
					Message: "v1",
				})
				require.NoError(t, err)

				cloneDir := t.TempDir()

				return App{
					cloneURL:  repoDir,
					reference: "v1",
					cloneDir:  cloneDir,
					srcPath:   path.Join(cloneDir, "remote-tag"),
					name:      "remote-tag",
				}
			},
		},
		{
			name: "ok: from git repo with branch",
			buildApp: func(t *testing.T) App {
				repoDir, repo := makeGitRepo(t, "remote-branch")
				w, err := repo.Worktree()
				require.NoError(t, err)
				err = w.Checkout(&git.CheckoutOptions{
					Branch: plumbing.NewBranchReferenceName("branch1"),
					Create: true,
				})
				require.NoError(t, err)

				cloneDir := t.TempDir()

				return App{
					cloneURL:  repoDir,
					reference: "branch1",
					cloneDir:  cloneDir,
					srcPath:   path.Join(cloneDir, "remote-branch"),
					name:      "remote-branch",
				}
			},
		},
		{
			name: "ok: from git repo with hash",
			buildApp: func(t *testing.T) App {
				repoDir, repo := makeGitRepo(t, "remote-hash")
				h, err := repo.Head()
				require.NoError(t, err)

				cloneDir := t.TempDir()

				return App{
					cloneURL:  repoDir,
					reference: h.Hash().String(),
					cloneDir:  cloneDir,
					srcPath:   path.Join(cloneDir, "remote-hash"),
					name:      "remote-hash",
				}
			},
		},
		{
			name: "fail: git ref not found",
			buildApp: func(t *testing.T) App {
				repoDir, _ := makeGitRepo(t, "remote-no-ref")

				cloneDir := t.TempDir()

				return App{
					cloneURL:  repoDir,
					reference: "doesnt_exists",
					cloneDir:  cloneDir,
					srcPath:   path.Join(cloneDir, "remote-no-ref"),
					name:      "remote-no-ref",
				}
			},
			expectedError: `cloning ".*": reference not found`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			require := require.New(t)
			assert := assert.New(t)
			p := tt.buildApp(t)
			defer p.KillClient()

			p.load(context.Background())

			if tt.expectedError != "" {
				require.Error(p.Error, "expected error %q", tt.expectedError)
				require.Regexp(tt.expectedError, p.Error.Error())
				return
			}

			require.NoError(p.Error)
			require.NotNil(p.Interface)
			manifest, err := p.Interface.Manifest(ctx)
			require.NoError(err)
			assert.Equal(p.name, manifest.Name)
			assert.NoError(p.Interface.Execute(ctx, &ExecutedCommand{OsArgs: []string{"ignite", p.name, "hello"}}, clientAPI))
			assert.NoError(p.Interface.ExecuteHookPre(ctx, &ExecutedHook{}, clientAPI))
			assert.NoError(p.Interface.ExecuteHookPost(ctx, &ExecutedHook{}, clientAPI))
			assert.NoError(p.Interface.ExecuteHookCleanUp(ctx, &ExecutedHook{}, clientAPI))
		})
	}
}

func TestAppLoadSharedHost(t *testing.T) {
	tests := []struct {
		name       string
		instances  int
		sharesHost bool
	}{
		{
			name:       "ok: from local sharedhost is on 1 instance",
			instances:  1,
			sharesHost: true,
		},
		{
			name:       "ok: from local sharedhost is on 2 instances",
			instances:  2,
			sharesHost: true,
		},
		{
			name:       "ok: from local sharedhost is on 4 instances",
			instances:  4,
			sharesHost: true,
		},
		{
			name:       "ok: from local sharedhost is off 4 instances",
			instances:  4,
			sharesHost: false,
		},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var (
				require = require.New(t)
				assert  = assert.New(t)
				// scaffold an unique app for all instances
				path = scaffoldApp(t, t.TempDir(),
					fmt.Sprintf("github.com/foo/bar-%d", i), tt.sharesHost)
				apps []*App
			)
			// Load one app per instance
			for i := 0; i < tt.instances; i++ {
				p := App{
					App:     appsconfig.App{Path: path},
					srcPath: path,
					name:    filepath.Base(path),
				}
				p.load(context.Background())
				require.NoError(p.Error)
				apps = append(apps, &p)
			}
			// Ensure all apps are killed at the end of test case
			defer func() {
				for i := len(apps) - 1; i >= 0; i-- {
					apps[i].KillClient()
					if tt.sharesHost && i > 0 {
						assert.False(apps[i].client.Exited(), "non host app can't kill host app")
						assert.True(checkConfCache(apps[i].Path), "non host app doesn't remove config cache when killed")
					} else {
						assert.True(apps[i].client.Exited(), "app should be killed")
					}
					assert.False(apps[i].isHost, "killed apps are no longer host")
				}
				assert.False(checkConfCache(apps[0].Path), "once host is killed the cache should be cleared")
			}()

			var hostConf *hplugin.ReattachConfig
			for i := 0; i < len(apps); i++ {
				if tt.sharesHost {
					assert.True(checkConfCache(apps[i].Path), "sharedHost must have a cache entry")
					if i == 0 {
						// first app is the host
						assert.True(apps[i].isHost, "first app is the host")
						// Assert reattach config has been saved
						hostConf = apps[i].client.ReattachConfig()
						ref, err := readConfigCache(apps[i].Path)
						if assert.NoError(err) {
							assert.Equal(hostConf, &ref, "wrong cache entry for app host")
						}
					} else {
						// apps after first aren't host
						assert.False(apps[i].isHost, "app %d can't be host", i)
						assert.Equal(hostConf, apps[i].client.ReattachConfig(), "ReattachConfig different from host app")
					}
				} else {
					assert.False(apps[i].isHost, "app %d can't be host if sharedHost is disabled", i)
					assert.False(checkConfCache(apps[i].Path), "app %d can't have a cache entry if sharedHost is disabled", i)
				}
			}
		})
	}
}

func TestAppClean(t *testing.T) {
	tests := []struct {
		name         string
		app          *App
		expectRemove bool
	}{
		{
			name: "dont clean local app",
			app: &App{
				App: appsconfig.App{Path: "/local"},
			},
		},
		{
			name: "dont clean app with errors",
			app:  &App{Error: errors.New("oups")},
		},
		{
			name: "ok",
			app: &App{
				cloneURL: "https://github.com/ignite/app",
			},
			expectRemove: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmp, err := os.MkdirTemp("", "cloneDir")
			require.NoError(t, err)
			tt.app.cloneDir = tmp

			err = tt.app.clean()

			require.NoError(t, err)
			if tt.expectRemove {
				_, err := os.Stat(tmp)
				assert.True(t, os.IsNotExist(err), "cloneDir not removed")
			}
		})
	}
}

// scaffoldApp runs Scaffold and updates the go.mod so it uses the
// current ignite/cli sources.
func scaffoldApp(t *testing.T, dir, name string, sharedHost bool) string {
	t.Helper()

	require := require.New(t)
	path, err := Scaffold(context.Background(), dir, name, sharedHost)
	require.NoError(err)

	// We want the scaffolded app to use the current version of ignite/cli,
	// for that we need to update the app go.mod and add a replace to target
	// current ignite/cli
	gomod, err := gomodule.ParseAt(path)
	require.NoError(err)

	// use GOMOD env to get current directory module path
	modpath, err := gocmd.Env(gocmd.EnvGOMOD)
	require.NoError(err)
	modpath = filepath.Dir(modpath)
	err = gomod.AddReplace("github.com/ignite/cli/v28", "", modpath, "")
	require.NoError(err)
	// Save go.mod
	data, err := gomod.Format()
	require.NoError(err)
	err = os.WriteFile(filepath.Join(path, "go.mod"), data, 0o644)
	require.NoError(err)
	return path
}

func assertApp(t *testing.T, want, have App) {
	t.Helper()

	if want.Error != nil {
		require.Error(t, have.Error)
		assert.Regexp(t, want.Error.Error(), have.Error.Error())
	} else {
		require.NoError(t, have.Error)
	}

	// Errors aren't comparable with assert.Equal, because of the different stacks
	want.Error = nil
	have.Error = nil
	assert.Equal(t, want, have)
}
