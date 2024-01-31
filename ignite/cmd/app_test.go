package ignitecmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	appsconfig "github.com/ignite/cli/v28/ignite/config/apps"
	"github.com/ignite/cli/v28/ignite/services/app"
	"github.com/ignite/cli/v28/ignite/services/app/mocks"
)

func buildRootCmd(ctx context.Context) *cobra.Command {
	var (
		rootCmd = &cobra.Command{
			Use: "ignite",
		}
		scaffoldCmd = &cobra.Command{
			Use: "scaffold",
		}
		scaffoldChainCmd = &cobra.Command{
			Use: "chain",
			Run: func(*cobra.Command, []string) {},
		}
		scaffoldModuleCmd = &cobra.Command{
			Use: "module",
			Run: func(*cobra.Command, []string) {},
		}
	)
	scaffoldChainCmd.Flags().String("path", "", "the path")
	scaffoldCmd.AddCommand(scaffoldChainCmd)
	scaffoldCmd.AddCommand(scaffoldModuleCmd)
	rootCmd.AddCommand(scaffoldCmd)
	rootCmd.SetContext(ctx)
	return rootCmd
}

func assertFlags(t *testing.T, expectedFlags []*app.Flag, execCmd *app.ExecutedCommand) {
	var (
		have     []string
		expected []string
	)

	t.Helper()

	flags, err := execCmd.NewFlags()
	assert.NoError(t, err)

	flags.VisitAll(func(f *pflag.Flag) {
		if f.Name == "help" {
			// ignore help flag
			return
		}

		have = append(have, f.Name)
	})

	for _, f := range expectedFlags {
		expected = append(expected, f.Name)
	}

	assert.Equal(t, expected, have)
}

func TestLinkAppCmds(t *testing.T) {
	var (
		args      = []string{"arg1", "arg2"}
		appParams = map[string]string{"key": "val"}
		// define a app with command flags
		appWithFlags = &app.Command{
			Use: "flaggy",
			Flags: []*app.Flag{
				{Name: "flag1", Type: app.FlagTypeString},
				{Name: "flag2", Type: app.FlagTypeInt, DefaultValue: "0"},
			},
		}
	)

	// helper to assert appInterface.Execute() calls
	expectExecute := func(t *testing.T, ctx context.Context, p *mocks.AppInterface, cmd *app.Command) {
		t.Helper()
		p.EXPECT().
			Execute(
				mock.Anything,
				mock.MatchedBy(func(execCmd *app.ExecutedCommand) bool {
					fmt.Println(cmd.Use == execCmd.Use, cmd.Use, execCmd.Use)
					return cmd.Use == execCmd.Use
				}),
				mock.Anything,
			).
			Run(func(_ context.Context, execCmd *app.ExecutedCommand, _ app.ClientAPI) {
				// Assert execCmd is populated correctly
				assert.True(t, strings.HasSuffix(execCmd.Path, cmd.Use), "wrong path %s", execCmd.Path)
				assert.Equal(t, args, execCmd.Args)
				assertFlags(t, cmd.Flags, execCmd)
				assert.Equal(t, appParams, execCmd.With)
			}).
			Return(nil)
	}

	tests := []struct {
		name            string
		setup           func(*testing.T, context.Context, *mocks.AppInterface)
		expectedDumpCmd string
		expectedError   string
	}{
		{
			name: "ok: link foo at root",
			setup: func(t *testing.T, ctx context.Context, p *mocks.AppInterface) {
				cmd := &app.Command{
					Use: "foo",
				}
				p.EXPECT().
					Manifest(ctx).
					Return(&app.Manifest{Commands: []*app.Command{cmd}}, nil)
				expectExecute(t, ctx, p, cmd)
			},
			expectedDumpCmd: `
ignite
  foo*
  scaffold
    chain* --path=string
    module*
`,
		},
		{
			name: "ok: link foo at subcommand",
			setup: func(t *testing.T, ctx context.Context, p *mocks.AppInterface) {
				cmd := &app.Command{
					Use:               "foo",
					PlaceCommandUnder: "ignite scaffold",
				}
				p.EXPECT().
					Manifest(ctx).
					Return(&app.Manifest{Commands: []*app.Command{cmd}}, nil)
				expectExecute(t, ctx, p, cmd)
			},
			expectedDumpCmd: `
ignite
  scaffold
    chain* --path=string
    foo*
    module*
`,
		},
		{
			name: "ok: link foo at subcommand with incomplete PlaceCommandUnder",
			setup: func(t *testing.T, ctx context.Context, p *mocks.AppInterface) {
				cmd := &app.Command{
					Use:               "foo",
					PlaceCommandUnder: "scaffold",
				}
				p.EXPECT().
					Manifest(ctx).
					Return(&app.Manifest{Commands: []*app.Command{cmd}}, nil)
				expectExecute(t, ctx, p, cmd)
			},
			expectedDumpCmd: `
ignite
  scaffold
    chain* --path=string
    foo*
    module*
`,
		},
		{
			name: "fail: link to runnable command",
			setup: func(t *testing.T, ctx context.Context, p *mocks.AppInterface) {
				p.EXPECT().
					Manifest(ctx).
					Return(&app.Manifest{
						Commands: []*app.Command{
							{
								Use:               "foo",
								PlaceCommandUnder: "ignite scaffold chain",
							},
						},
					},
						nil,
					)
			},
			expectedError: `can't attach app command "foo" to runnable command "ignite scaffold chain"`,
		},
		{
			name: "fail: link to unknown command",
			setup: func(t *testing.T, ctx context.Context, p *mocks.AppInterface) {
				p.EXPECT().
					Manifest(ctx).
					Return(&app.Manifest{
						Commands: []*app.Command{
							{
								Use:               "foo",
								PlaceCommandUnder: "ignite unknown",
							},
						},
					},
						nil,
					)
			},
			expectedError: `unable to find command path "ignite unknown" for app "foo"`,
		},
		{
			name: "fail: app name exists in legacy commands",
			setup: func(t *testing.T, ctx context.Context, p *mocks.AppInterface) {
				p.EXPECT().
					Manifest(ctx).
					Return(&app.Manifest{
						Commands: []*app.Command{
							{
								Use: "scaffold",
							},
						},
					},
						nil,
					)
			},
			expectedError: `app command "scaffold" already exists in Ignite's commands`,
		},
		{
			name: "fail: app name with args exists in legacy commands",
			setup: func(t *testing.T, ctx context.Context, p *mocks.AppInterface) {
				p.EXPECT().
					Manifest(ctx).
					Return(&app.Manifest{
						Commands: []*app.Command{
							{
								Use: "scaffold [args]",
							},
						},
					},
						nil,
					)
			},
			expectedError: `app command "scaffold" already exists in Ignite's commands`,
		},
		{
			name: "fail: app name exists in legacy sub commands",
			setup: func(t *testing.T, ctx context.Context, p *mocks.AppInterface) {
				p.EXPECT().
					Manifest(ctx).
					Return(&app.Manifest{
						Commands: []*app.Command{
							{
								Use:               "chain",
								PlaceCommandUnder: "scaffold",
							},
						},
					},
						nil,
					)
			},
			expectedError: `app command "chain" already exists in Ignite's commands`,
		},
		{
			name: "ok: link multiple at root",
			setup: func(t *testing.T, ctx context.Context, p *mocks.AppInterface) {
				fooCmd := &app.Command{
					Use: "foo",
				}
				barCmd := &app.Command{
					Use: "bar",
				}
				p.EXPECT().
					Manifest(ctx).
					Return(&app.Manifest{
						Commands: []*app.Command{
							fooCmd, barCmd, appWithFlags,
						},
					}, nil)
				expectExecute(t, ctx, p, fooCmd)
				expectExecute(t, ctx, p, barCmd)
				expectExecute(t, ctx, p, appWithFlags)
			},
			expectedDumpCmd: `
ignite
  bar*
  flaggy* --flag1=string --flag2=int
  foo*
  scaffold
    chain* --path=string
    module*
`,
		},
		{
			name: "ok: link with subcommands",
			setup: func(t *testing.T, ctx context.Context, p *mocks.AppInterface) {
				cmd := &app.Command{
					Use: "foo",
					Commands: []*app.Command{
						{Use: "bar"},
						{Use: "baz"},
						appWithFlags,
					},
				}
				p.EXPECT().
					Manifest(ctx).
					Return(&app.Manifest{Commands: []*app.Command{cmd}}, nil)
				// cmd is not executed because it's not runnable, only sub-commands
				// are executed.
				expectExecute(t, ctx, p, cmd.Commands[0])
				expectExecute(t, ctx, p, cmd.Commands[1])
				expectExecute(t, ctx, p, cmd.Commands[2])
			},
			expectedDumpCmd: `
ignite
  foo
    bar*
    baz*
    flaggy* --flag1=string --flag2=int
  scaffold
    chain* --path=string
    module*
`,
		},
		{
			name: "ok: link with multiple subcommands",
			setup: func(t *testing.T, ctx context.Context, p *mocks.AppInterface) {
				cmd := &app.Command{
					Use: "foo",
					Commands: []*app.Command{
						{Use: "bar", Commands: []*app.Command{{Use: "baz"}}},
						{Use: "qux", Commands: []*app.Command{{Use: "quux"}, {Use: "corge"}}},
					},
				}
				p.EXPECT().
					Manifest(ctx).
					Return(&app.Manifest{Commands: []*app.Command{cmd}}, nil)
				expectExecute(t, ctx, p, cmd.Commands[0].Commands[0])
				expectExecute(t, ctx, p, cmd.Commands[1].Commands[0])
				expectExecute(t, ctx, p, cmd.Commands[1].Commands[1])
			},
			expectedDumpCmd: `
ignite
  foo
    bar
      baz*
    qux
      corge*
      quux*
  scaffold
    chain* --path=string
    module*
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			require := require.New(t)
			assert := assert.New(t)
			pi := mocks.NewAppInterface(t)
			p := &app.App{
				App: appsconfig.App{
					Path: "foo",
					With: appParams,
				},
				Interface: pi,
			}
			rootCmd := buildRootCmd(ctx)
			tt.setup(t, ctx, pi)

			_ = linkApps(ctx, rootCmd, []*app.App{p})

			if tt.expectedError != "" {
				require.Error(p.Error)
				require.EqualError(p.Error, tt.expectedError)
				return
			}
			require.NoError(p.Error)
			var s strings.Builder
			s.WriteString("\n")
			dumpCmd(rootCmd, &s, 0)
			assert.Equal(tt.expectedDumpCmd, s.String())
			execCmd(t, rootCmd, args)
		})
	}
}

// dumpCmd helps in comparing cobra.Command by writing their Use and Commands.
// Runnable commands are marked with a *.
func dumpCmd(c *cobra.Command, w io.Writer, ntabs int) {
	fmt.Fprintf(w, "%s%s", strings.Repeat("  ", ntabs), c.Use)
	ntabs++
	if c.Runnable() {
		fmt.Fprintf(w, "*")
	}
	c.Flags().VisitAll(func(f *pflag.Flag) {
		fmt.Fprintf(w, " --%s=%s", f.Name, f.Value.Type())
	})
	fmt.Fprintf(w, "\n")
	for _, cc := range c.Commands() {
		dumpCmd(cc, w, ntabs)
	}
}

func TestLinkAppHooks(t *testing.T) {
	var (
		args      = []string{"arg1", "arg2"}
		appParams = map[string]string{"key": "val"}
		ctx       = context.Background()

		// helper to assert appInterface.ExecuteHook*() calls in expected order
		// (pre, then post, then cleanup)
		expectExecuteHook = func(t *testing.T, p *mocks.AppInterface, expectedFlags []*app.Flag, hooks ...*app.Hook) {
			matcher := func(hook *app.Hook) any {
				return mock.MatchedBy(func(execHook *app.ExecutedHook) bool {
					return hook.Name == execHook.Hook.Name &&
						hook.PlaceHookOn == execHook.Hook.PlaceHookOn
				})
			}
			asserter := func(hook *app.Hook) func(_ context.Context, hook *app.ExecutedHook, _ app.ClientAPI) {
				return func(_ context.Context, execHook *app.ExecutedHook, _ app.ClientAPI) {
					assert.True(t, strings.HasSuffix(execHook.ExecutedCommand.Path, hook.PlaceHookOn), "wrong path %q want %q", execHook.ExecutedCommand.Path, hook.PlaceHookOn)
					assert.Equal(t, args, execHook.ExecutedCommand.Args)
					assertFlags(t, expectedFlags, execHook.ExecutedCommand)
					assert.Equal(t, appParams, execHook.ExecutedCommand.With)
				}
			}
			var lastPre *mock.Call
			for _, hook := range hooks {
				pre := p.EXPECT().
					ExecuteHookPre(ctx, matcher(hook), mock.Anything).
					Run(asserter(hook)).
					Return(nil).
					Call
				if lastPre != nil {
					pre.NotBefore(lastPre)
				}
				lastPre = pre
			}
			for _, hook := range hooks {
				post := p.EXPECT().
					ExecuteHookPost(ctx, matcher(hook), mock.Anything).
					Run(asserter(hook)).
					Return(nil).
					Call
				cleanup := p.EXPECT().
					ExecuteHookCleanUp(ctx, matcher(hook), mock.Anything).
					Run(asserter(hook)).
					Return(nil).
					Call
				post.NotBefore(lastPre)
				cleanup.NotBefore(post)
			}
		}
	)
	tests := []struct {
		name          string
		expectedError string
		setup         func(*testing.T, context.Context, *mocks.AppInterface)
	}{
		{
			name: "fail: command not runnable",
			setup: func(t *testing.T, ctx context.Context, p *mocks.AppInterface) {
				p.EXPECT().
					Manifest(ctx).
					Return(&app.Manifest{
						Hooks: []*app.Hook{
							{
								Name:        "test-hook",
								PlaceHookOn: "ignite scaffold",
							},
						},
					},
						nil,
					)
			},
			expectedError: `can't attach app hook "test-hook" to non executable command "ignite scaffold"`,
		},
		{
			name: "fail: command doesn't exists",
			setup: func(t *testing.T, ctx context.Context, p *mocks.AppInterface) {
				p.EXPECT().
					Manifest(ctx).
					Return(&app.Manifest{
						Hooks: []*app.Hook{
							{
								Name:        "test-hook",
								PlaceHookOn: "ignite chain",
							},
						},
					},
						nil,
					)
			},
			expectedError: `unable to find command path "ignite chain" for app hook "test-hook"`,
		},
		{
			name: "ok: single hook",
			setup: func(t *testing.T, ctx context.Context, p *mocks.AppInterface) {
				hook := &app.Hook{
					Name:        "test-hook",
					PlaceHookOn: "scaffold chain",
				}
				p.EXPECT().
					Manifest(ctx).
					Return(&app.Manifest{Hooks: []*app.Hook{hook}}, nil)
				expectExecuteHook(t, p, []*app.Flag{{Name: "path"}}, hook)
			},
		},
		{
			name: "ok: multiple hooks on same command",
			setup: func(t *testing.T, ctx context.Context, p *mocks.AppInterface) {
				hook1 := &app.Hook{
					Name:        "test-hook-1",
					PlaceHookOn: "scaffold chain",
				}
				hook2 := &app.Hook{
					Name:        "test-hook-2",
					PlaceHookOn: "scaffold chain",
				}
				p.EXPECT().
					Manifest(ctx).
					Return(&app.Manifest{Hooks: []*app.Hook{hook1, hook2}}, nil)
				expectExecuteHook(t, p, []*app.Flag{{Name: "path"}}, hook1, hook2)
			},
		},
		{
			name: "ok: multiple hooks on different commands",
			setup: func(t *testing.T, ctx context.Context, p *mocks.AppInterface) {
				hookChain1 := &app.Hook{
					Name:        "test-hook-1",
					PlaceHookOn: "scaffold chain",
				}
				hookChain2 := &app.Hook{
					Name:        "test-hook-2",
					PlaceHookOn: "scaffold chain",
				}
				hookModule := &app.Hook{
					Name:        "test-hook-3",
					PlaceHookOn: "scaffold module",
				}
				p.EXPECT().
					Manifest(ctx).
					Return(&app.Manifest{Hooks: []*app.Hook{hookChain1, hookChain2, hookModule}}, nil)
				expectExecuteHook(t, p, []*app.Flag{{Name: "path"}}, hookChain1, hookChain2)
				expectExecuteHook(t, p, nil, hookModule)
			},
		},
		{
			name: "ok: duplicate hook names on same command",
			setup: func(t *testing.T, ctx context.Context, p *mocks.AppInterface) {
				hooks := []*app.Hook{
					{
						Name:        "test-hook",
						PlaceHookOn: "ignite scaffold chain",
					},
					{
						Name:        "test-hook",
						PlaceHookOn: "ignite scaffold chain",
					},
				}
				p.EXPECT().
					Manifest(ctx).
					Return(&app.Manifest{Hooks: hooks}, nil)
				expectExecuteHook(t, p, []*app.Flag{{Name: "path"}}, hooks...)
			},
		},
		{
			name: "ok: duplicate hook names on different commands",
			setup: func(t *testing.T, ctx context.Context, p *mocks.AppInterface) {
				hookChain := &app.Hook{
					Name:        "test-hook",
					PlaceHookOn: "ignite scaffold chain",
				}
				hookModule := &app.Hook{
					Name:        "test-hook",
					PlaceHookOn: "ignite scaffold module",
				}
				p.EXPECT().
					Manifest(ctx).
					Return(&app.Manifest{Hooks: []*app.Hook{hookChain, hookModule}}, nil)
				expectExecuteHook(t, p, []*app.Flag{{Name: "path"}}, hookChain)
				expectExecuteHook(t, p, nil, hookModule)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			require := require.New(t)
			// assert := assert.New(t)
			pi := mocks.NewAppInterface(t)
			p := &app.App{
				App: appsconfig.App{
					Path: "foo",
					With: appParams,
				},
				Interface: pi,
			}
			rootCmd := buildRootCmd(ctx)
			tt.setup(t, ctx, pi)

			_ = linkApps(ctx, rootCmd, []*app.App{p})

			if tt.expectedError != "" {
				require.EqualError(p.Error, tt.expectedError)
				return
			}
			require.NoError(p.Error)
			execCmd(t, rootCmd, args)
		})
	}
}

// execCmd executes all the runnable commands contained in c.
func execCmd(t *testing.T, c *cobra.Command, args []string) {
	if c.Runnable() {
		os.Args = strings.Fields(c.CommandPath())
		os.Args = append(os.Args, args...)
		err := c.Execute()
		require.NoError(t, err)
		return
	}
	for _, c := range c.Commands() {
		execCmd(t, c, args)
	}
}
