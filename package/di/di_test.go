package di_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lithammer/dedent"
	"github.com/livebud/bud/internal/imports"
	"github.com/livebud/bud/internal/is"
	"github.com/livebud/bud/package/di"
	"github.com/livebud/bud/package/gomod"
	"github.com/livebud/bud/package/log/testlog"
	"github.com/livebud/bud/package/modcache"
	"github.com/livebud/bud/package/parser"
	"github.com/livebud/bud/package/vfs"
	"github.com/matthewmueller/diff"
)

func redent(s string) string {
	return strings.TrimSpace(dedent.Dedent(s)) + "\n"
}

func goRun(ctx context.Context, cacheDir, appDir string) (string, error) {
	cmd := exec.CommandContext(ctx, "go", "run", "-mod", "mod", "main.go")
	cmd.Env = append(os.Environ(), "GOMODCACHE="+cacheDir, "GOPRIVATE=*")
	stdout := new(bytes.Buffer)
	cmd.Stdout = stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	cmd.Dir = appDir
	err := cmd.Run()
	if err != nil {
		return "", err
	}
	return stdout.String(), nil
}

type Test struct {
	Function *di.Function
	Files    map[string]string
	Expect   string
}

func runTest(t testing.TB, test Test) {
	t.Helper()
	is := is.New(t)
	log := testlog.New()
	ctx := context.Background()
	appDir := t.TempDir()
	appFS := os.DirFS(appDir)
	modCache := modcache.Default()
	// Write application files
	if test.Files != nil {
		vmap := vfs.Map{}
		for path, code := range test.Files {
			vmap[path] = []byte(redent(code))
		}
		err := vfs.Write(appDir, vmap)
		is.NoErr(err)
	}
	module, err := gomod.Find(appDir, gomod.WithModCache(modCache))
	is.NoErr(err)
	parser := parser.New(appFS, module)
	injector := di.New(appFS, log, module, parser)
	node, err := injector.Load(test.Function)
	if err != nil {
		is.Equal(test.Expect, err.Error())
		return
	}
	provider := node.Generate(imports.New(), test.Function.Name, test.Function.Target)
	code := provider.File()
	// TODO: provide a module method for doing this, module.ResolveDirectory
	// also stats the final dir, which doesn't exist yet.
	targetDir := module.Directory(strings.TrimPrefix(test.Function.Target, module.Import()))
	err = os.MkdirAll(targetDir, 0755)
	is.NoErr(err)
	outPath := filepath.Join(targetDir, "di.go")
	err = os.WriteFile(outPath, []byte(code), 0644)
	is.NoErr(err)
	stdout, err := goRun(ctx, modCache.Directory(), appDir)
	is.NoErr(err)
	diff.TestString(t, redent(test.Expect), stdout)
}

const goMod = `module app.com

go 1.17

require (
  github.com/hexops/valast v1.4.1
)
`

const mainGo = `package main

import (
  "os"
  "fmt"
  "github.com/hexops/valast"
  "app.com/gen/web"
)

func main() {
  actual := web.Load()
  fmt.Fprintf(os.Stdout, "%s\n", valast.String(actual))
}
`

const mainGoFmt = `package main

import (
  "os"
  "fmt"
  "app.com/gen/web"
)

func main() {
  actual := web.Load()
  fmt.Fprintf(os.Stdout, "%s\n", actual)
}
`

const mainGoWithErr = `package main

import (
  "os"
  "fmt"
  "github.com/hexops/valast"
  "app.com/gen/web"
)

func main() {
  actual, err := web.Load()
  if err != nil {
    fmt.Fprintf(os.Stdout, "%s\n", err)
    return
  }
  fmt.Fprintf(os.Stdout, "%s\n", valast.String(actual))
}
`

func TestFunctionAll(t *testing.T) {
	runTest(t, Test{
		Function: &di.Function{
			Name:   "Load",
			Target: "app.com/gen/web",
			Results: []di.Dependency{
				di.ToType("app.com/web", "*Web"),
				&di.Error{},
			},
		},
		Expect: `
			&web.Web{
				r: &router.Router{},
				c: &controller.Map{
					pages: &pages.Controller{log: &log.Log{
						e: &env.Env{},
					}},
					users: &users.Controller{
						db: &db.DB{
							env: &env.Env{},
							log: &log.Log{e: &env.Env{}},
						},
						log: &log.Log{e: &env.Env{}},
					},
				},
			}
		`,
		Files: map[string]string{
			"go.mod":  goMod,
			"main.go": mainGoWithErr,
			"web/web.go": `
				package web

				import (
					"app.com/controller"
					"app.com/router"
				)

				// New web
				func New(r *router.Router, c *controller.Map) *Web {
					return &Web{r, c}
				}

				// Web struct
				type Web struct {
					r *router.Router
					c *controller.Map
				}
			`,
			"env/env.go": `
				package env

				// New env
				func New() (*Env, error) {
					return &Env{}, nil
				}

				// Env struct
				type Env struct {
					LogLevel    string
					PostgresURL string
				}
			`,
			"log/log.go": `
				package log

				import (
					"app.com/env"
				)

				// Log struct
				type Log struct {
					e *env.Env
				}

				// New Log
				func New(e *env.Env) (*Log, error) {
					return &Log{e}, nil
				}
			`,
			"router/router.go": `
				package router

				// New router
				func New() *Router {
					return &Router{}
				}

				// Router struct
				type Router struct {
				}
			`,
			"db/db.go": `
				package db

				import (
					"app.com/env"
					"app.com/log"
				)

				// New fn
				func New(e *env.Env, log *log.Log) (*DB, error) {
					return &DB{e, log}, nil
				}

				// DB Struct
				type DB struct {
					env  *env.Env
					log  *log.Log
				}
			`,
			"controllers/users/controller.go": `
				package users

				import (
					"app.com/db"
					"app.com/log"
				)

				// New controller
				func New(db *db.DB, log *log.Log) *Controller {
					return &Controller{db, log}
				}

				// Controller struct
				type Controller struct {
					db *db.DB
					log *log.Log
				}
			`,
			"controllers/pages/controller.go": `
				package pages

				import (
					"app.com/log"
				)

				// New controller
				func New(log *log.Log) *Controller {
					return &Controller{log}
				}

				// Controller struct
				type Controller struct {
					log *log.Log
				}
			`,
			"controller/controller.go": `
				package controller

				import (
					"app.com/controllers/pages"
					"app.com/controllers/users"
				)

				// New controller map
				func New(pages *pages.Controller, users *users.Controller) *Map {
					return &Map{pages, users}
				}

				// Map of controllers
				type Map struct {
					pages *pages.Controller
					users *users.Controller
				}
			`,
		},
	})
}

func TestFunctionNeedsDeref(t *testing.T) {
	runTest(t, Test{
		Function: &di.Function{
			Name:   "Load",
			Target: "app.com/gen/web",
			Results: []di.Dependency{
				di.ToType("app.com/web", "*Web"),
			},
		},
		Expect: `
			&web.Web{m: web.Middleware{
				v: "v",
			}}
		`,
		Files: map[string]string{
			"go.mod":  goMod,
			"main.go": mainGo,
			"web/web.go": `
				package web

				// Middlewares function
				func Middlewares() *Middleware {
					return &Middleware{"v"}
				}

				// Middleware handler
				type Middleware struct{
					v string
				}

				// New web
				func New(m Middleware) *Web {
					return &Web{m}
				}

				// Web struct
				type Web struct {
					m Middleware
				}
			`,
		},
	})
}

func TestFunctionNeedsPointer(t *testing.T) {
	runTest(t, Test{
		Function: &di.Function{
			Name:   "Load",
			Target: "app.com/gen/web",
			Results: []di.Dependency{
				di.ToType("app.com/web", "*Web"),
			},
		},
		Expect: `
			&web.Web{m: &web.Middleware{
				v: "v",
			}}
		`,
		Files: map[string]string{
			"go.mod":  goMod,
			"main.go": mainGo,
			"web/web.go": `
				package web

				// Middlewares function
				func Middlewares() Middleware {
					return Middleware{"v"}
				}

				// Middleware handler
				type Middleware struct{
					v string
				}

				// New web
				func New(m *Middleware) *Web {
					return &Web{m}
				}

				// Web struct
				type Web struct {
					m *Middleware
				}
			`,
		},
	})
}

func TestFunctionHasError(t *testing.T) {
	runTest(t, Test{
		Function: &di.Function{
			Name:   "Load",
			Target: "app.com/gen/web",
			Results: []di.Dependency{
				di.ToType("app.com/web", "*Web"),
				&di.Error{},
			},
		},
		Expect: `env: unable to load environment`,
		Files: map[string]string{
			"go.mod":  goMod,
			"main.go": mainGoWithErr,
			"env/env.go": `
				package env

				import (
					"errors"
				)

				// New env
				func New() (*Env, error) {
					return &Env{}, errors.New("env: unable to load environment")
				}

				// Env struct
				type Env struct {
					LogLevel    string
					PostgresURL string
				}
			`,
			"log/log.go": `
				package log

				import (
					"app.com/env"
				)

				// Log struct
				type Log struct {
					e *env.Env
				}

				// New Log
				func New(e *env.Env) (*Log, error) {
					return &Log{e}, nil
				}
			`,
			"web/web.go": `
				package web

				import (
					"app.com/log"
				)

				// New web
				func New(log *log.Log) *Web {
					return &Web{log}
				}

				// Web struct
				type Web struct {
					l *log.Log
				}
			`,
		},
	})
}

func TestStructAll(t *testing.T) {
	runTest(t, Test{
		Function: &di.Function{
			Name:   "Load",
			Target: "app.com/gen/web",
			Results: []di.Dependency{
				di.ToType("app.com/web", "*Web"),
			},
		},
		Expect: `
			&web.Web{
				Blank: &web.Blank{
					small: "s",
				},
				Database: &web.Database{DB: &db.DB{
					Env: &env.Env{},
					Log: &log.Log{Env: &env.Env{}},
				}},
				Env: &env.Env{},
				Users: &users.Controller{
					DB: &db.DB{
						Env: &env.Env{},
						Log: &log.Log{Env: &env.Env{}},
					},
					Env: &env.Env{},
				},
			}
		`,
		Files: map[string]string{
			"go.mod":  goMod,
			"main.go": mainGo,
			"web/web.go": `
				package web

				import (
					"app.com/db"
					"app.com/env"
					"app.com/users"
				)

				func NewBlank() *Blank {
					return &Blank{"s"}
				}

				// Blank struct
				type Blank struct {
					small string
				}

				// Database struct
				type Database struct {
					*db.DB
				}

				// Web struct
				type Web struct {
					*Blank
					*Database
					*env.Env
					Users *users.Controller
				}
			`,
			"users/controller.go": `
				package users

				import (
					"app.com/db"
					"app.com/env"
				)

				// Controller struct
				type Controller struct {
					*db.DB
					Env *env.Env
				}
			`,
			"log/log.go": `
				package log

				import (
					"app.com/env"
				)

				// Log struct
				type Log struct {
					*env.Env
				}
			`,
			"env/env.go": `
				package env

				// Env struct
				type Env struct {}
			`,
			"db/db.go": `
				package db

				import (
					"app.com/env"
					"app.com/log"
				)

				// DB Struct
				type DB struct {
					Env *env.Env
					Log *log.Log
				}
			`,
		},
	})
}

func TestStructNeedsDeref(t *testing.T) {
	runTest(t, Test{
		Function: &di.Function{
			Name:   "Load",
			Target: "app.com/gen/web",
			Results: []di.Dependency{
				di.ToType("app.com/web", "*Web"),
			},
		},
		Expect: `
			&web.Web{Public: public.Middleware{
				A: public.String("a"),
			}}
		`,
		Files: map[string]string{
			"go.mod":  goMod,
			"main.go": mainGo,
			"web/web.go": `
				package web

				import (
					"app.com/public"
				)

				// Web struct
				type Web struct {
					Public public.Middleware
				}
			`,
			"public/public.go": `
				package public

				func New() *Middleware {
					return &Middleware{A: String("a")}
				}

				type Middleware struct {
					A String
				}

				type String string
			`,
		},
	})
}

func TestStructNeedsPointer(t *testing.T) {
	runTest(t, Test{
		Function: &di.Function{
			Name:   "Load",
			Target: "app.com/gen/web",
			Results: []di.Dependency{
				di.ToType("app.com/web", "*Web"),
			},
		},
		Expect: `
			&web.Web{Public: &public.Middleware{
				A: public.String("a"),
			}}
		`,
		Files: map[string]string{
			"go.mod":  goMod,
			"main.go": mainGo,
			"web/web.go": `
				package web

				import (
					"app.com/public"
				)

				// Web struct
				type Web struct {
					Public *public.Middleware
				}
			`,
			"public/public.go": `
				package public

				func New() Middleware {
					return Middleware{A: String("a")}
				}

				type Middleware struct {
					A String
				}

				type String string
			`,
		},
	})
}

func TestNestedModules(t *testing.T) {
	t.SkipNow()
	runTest(t, Test{
		Function: &di.Function{
			Name:   "Load",
			Target: "app.com/gen/web",
			Results: []di.Dependency{
				di.ToType("app.com/one", "*One"),
			},
		},
		Expect: `
			&One{Two: &Two{Three: Three{}}}
		`,
		// Modules: map[string]map[string]string{
		// 	"mod.test/three@v1.0.0": map[string]string{
		// 		"inner/inner.go": `
		// 			package inner

		// 			import (
		// 				"fmt"
		// 			)

		// 			type Three struct {}

		// 			func (t Three) String() string {
		// 				return fmt.Sprintf("Three{}")
		// 			}
		// 		`,
		// 	},
		// 	"mod.test/two@v0.0.1": map[string]string{
		// 		"struct.go": `
		// 			package two

		// 			type Struct struct {
		// 			}
		// 		`,
		// 	},
		// 	"mod.test/two@v0.0.2": map[string]string{
		// 		"go.mod": `
		// 			module mod.test/two

		// 			require (
		// 				mod.test/three v1.0.0
		// 			)
		// 		`,
		// 		"struct.go": `
		// 			package two

		// 			import (
		// 				"mod.test/three/inner"
		// 				"fmt"
		// 			)

		// 			type Two struct {
		// 				inner.Three
		// 			}

		// 			func (t *Two) String() string {
		// 				return fmt.Sprintf("&Two{Three: %s}", t.Three)
		// 			}
		// 		`,
		// 	},
		// },
		Files: map[string]string{
			"main.go": mainGoFmt,
			"go.mod": `
				module app.com

				require (
					mod.test/two v0.0.2
				)
			`,
			"one/one.go": `
				package one

				import (
					"mod.test/two"
					"fmt"
				)

				type One struct {
					Two *two.Two
				}

				func (o *One) String() string {
					return fmt.Sprintf("&One{Two: %s}", o.Two)
				}
			`,
		},
	})
}

func TestAlias(t *testing.T) {
	runTest(t, Test{
		Function: &di.Function{
			Name:   "Load",
			Target: "app.com/gen/web",
			Results: []di.Dependency{
				di.ToType("app.com/web", "*Web"),
			},
		},
		Expect: `
			&web.Web{Public: &public.publicMiddleware{}}
		`,
		Files: map[string]string{
			"go.mod":  goMod,
			"main.go": mainGo,
			"web/web.go": `
				package web

				import (
					"app.com/public"
				)

				// Web struct
				type Web struct {
					Public public.Middleware
				}
			`,
			"public/public.go": `
				package public

				import (
					"net/http"
					"app.com/middleware"
				)

				func New() Middleware {
					return &publicMiddleware{}
				}

				type Middleware = middleware.Middleware

				type publicMiddleware struct{
				}

				func (p *publicMiddleware) Middleware(handler http.Handler) http.Handler {
					return handler
				}
			`,
			"middleware/middleware.go": `
				package middleware

				import (
					"net/http"
				)

				type Middleware interface {
					Middleware(handler http.Handler) http.Handler
				}
			`,
		},
	})
}

func TestAliasPointer(t *testing.T) {
	runTest(t, Test{
		Function: &di.Function{
			Name:   "Load",
			Target: "app.com/gen/web",
			Results: []di.Dependency{
				di.ToType("app.com/web", "*Web"),
			},
		},
		Expect: `
			&web.Web{Public: &middleware.Middleware{}}
		`,
		Files: map[string]string{
			"go.mod":  goMod,
			"main.go": mainGo,
			"web/web.go": `
				package web

				import (
					"app.com/public"
				)

				// Web struct
				type Web struct {
					Public *public.Middleware
				}
			`,
			"public/public.go": `
				package public

				import (
					"app.com/middleware"
				)

				func New() *Middleware {
					return &Middleware{}
				}

				type Middleware = middleware.Middleware
			`,
			"middleware/middleware.go": `
				package middleware

				type Middleware struct {}
			`,
		},
	})
}

func TestExternalInFile(t *testing.T) {
	runTest(t, Test{
		Function: &di.Function{
			Name:   "Load",
			Target: "app.com/gen/web",
			Params: []*di.Param{
				{Import: "app.com/web", Type: "*A"},
				{Import: "app.com/web", Type: "*B"},
			},
			Results: []di.Dependency{
				di.ToType("app.com/web", "*Web"),
			},
		},
		Expect: `
			&web.Web{
				a: &web.A{
					Value: "A",
				},
				b: &web.B{Value: "B"},
			}
		`,
		Files: map[string]string{
			"go.mod": goMod,
			"main.go": `
				package main

				import (
					"os"
					"fmt"
					"github.com/hexops/valast"
					web "app.com/web"
					genweb "app.com/gen/web"
				)

				func main() {
					actual := genweb.Load(&web.A{"A"}, &web.B{"B"})
					fmt.Fprintf(os.Stdout, "%s\n", valast.String(actual))
				}
			`,
			"web/web.go": `
				package web

				type A struct {
					Value string
				}

				type B struct {
					Value string
				}

				// New web
				func New(a *A, b *B) *Web {
					return &Web{a, b}
				}

				// Web struct
				type Web struct {
					a *A
					b *B
				}
			`,
		},
	})
}

func TestExternalShared(t *testing.T) {
	runTest(t, Test{
		Function: &di.Function{
			Name:   "Load",
			Target: "app.com/gen/web",
			Params: []*di.Param{
				{Import: "app.com/web", Type: "*A"},
				{Import: "app.com/web", Type: "*B"},
				{Import: "app.com/web", Type: "*C"},
			},
			Results: []di.Dependency{
				di.ToType("app.com/web", "*Web"),
			},
		},
		Expect: `
			&web.Web{
				a: &web.A{
					Value: "A",
					B:     &web.B{Value: "B"},
					C:     &web.C{Value: "C"},
				},
				c: &web.C{Value: "C"},
			}
		`,
		Files: map[string]string{
			"go.mod": goMod,
			"main.go": `
				package main

				import (
					"os"
					"fmt"
					"github.com/hexops/valast"
					web "app.com/web"
					genweb "app.com/gen/web"
				)

				func main() {
					c := &web.C{"C"}
					actual := genweb.Load(&web.A{"A", &web.B{"B"}, c}, c)
					fmt.Fprintf(os.Stdout, "%s\n", valast.String(actual))
				}
			`,
			"web/web.go": `
				package web

				type A struct {
					Value string
					*B
					*C
				}

				type B struct {
					Value string
				}

				type C struct {
					Value string
				}

				// New web
				func New(a *A, c *C) *Web {
					return &Web{a, c}
				}

				// Web struct
				type Web struct {
					a *A
					c *C
				}
			`,
		},
	})
}

func TestExternalStdlib(t *testing.T) {
	runTest(t, Test{
		Function: &di.Function{
			Name:   "Load",
			Target: "app.com/gen/web",
			Params: []*di.Param{
				{Import: "net/http", Type: "*Request"},
			},
			Results: []di.Dependency{
				di.ToType("app.com/web", "*Web"),
			},
		},
		Expect: `
			&web.Web{Request: &http.Request{
				Method:     "GET",
				URL:        &url.URL{Path: "/"},
				Proto:      "HTTP/1.1",
				ProtoMajor: 1,
				ProtoMinor: 1,
				Header:     http.Header{},
				ctx:        valast.Addr(context.emptyCtx(0)).(*context.emptyCtx),
			}}
		`,
		Files: map[string]string{
			"go.mod": goMod,
			"main.go": `
				package main

				import (
					"os"
					"fmt"
					"net/http"
					"github.com/hexops/valast"
					genweb "app.com/gen/web"
				)

				func main() {
					request, err := http.NewRequest("GET", "/", nil)
					if err != nil {
						fmt.Fprintf(os.Stderr, "%s\n", err.Error())
						return
					}
					actual := genweb.Load(request)
					fmt.Fprintf(os.Stdout, "%s\n", valast.String(actual))
				}
			`,
			"web/web.go": `
				package web

				import (
					"net/http"
				)

				// Web struct
				type Web struct {
					*http.Request
				}
			`,
		},
	})
}

func TestExternalUnused(t *testing.T) {
	runTest(t, Test{
		Function: &di.Function{
			Name:   "Load",
			Target: "app.com/gen/web",
			Params: []*di.Param{
				{Import: "app.com/web", Type: "*A"},
				{Import: "app.com/web", Type: "*B"},
			},
			Results: []di.Dependency{
				di.ToType("app.com/web", "*Web"),
			},
		},
		Expect: `
			&web.Web{a: &web.A{
				Value: "A",
			}}
		`,
		Files: map[string]string{
			"go.mod": goMod,
			"main.go": `
				package main

				import (
					"os"
					"fmt"
					"github.com/hexops/valast"
					"app.com/web"
					genweb "app.com/gen/web"
				)

				func main() {
					actual := genweb.Load(&web.A{"A"})
					fmt.Fprintf(os.Stdout, "%s\n", valast.String(actual))
				}
			`,
			"web/web.go": `
				package web

				type A struct {
					Value string
				}

				type B struct {
					Value string
				}

				// New web
				func New(a *A) *Web {
					return &Web{a}
				}

				// Web struct
				type Web struct {
					a *A
				}
			`,
		},
	})
}

func TestHoistFull(t *testing.T) {
	runTest(t, Test{
		Function: &di.Function{
			Name:   "Load",
			Hoist:  true,
			Target: "app.com/gen/web",
			Params: []*di.Param{
				{Import: "app.com/web", Type: "*Request"},
			},
			Results: []di.Dependency{
				di.ToType("app.com/web", "*Web"),
			},
		},
		Expect: `
			&web.Web{
				Session: &web.Session{
					Request: &web.Request{},
					DB: &web.Postgres{Log: &web.Log{
						value: "log",
						Env:   &web.Env{value: "env"},
					}},
				},
				Log: &web.Log{
					value: "log",
					Env:   &web.Env{value: "env"},
				},
				Workflow: &web.Workflow{Log: &web.Log{
					value: "log",
					Env:   &web.Env{value: "env"},
				}},
			}
		`,
		Files: map[string]string{
			"go.mod": goMod,
			"main.go": `
				package main

				import (
					"os"
					"fmt"
					"github.com/hexops/valast"
					web "app.com/web"
					genweb "app.com/gen/web"
				)

				func main() {
					request := &web.Request{}
					env := web.NewEnv()
					log := web.NewLog(env)
					pg := &web.Postgres{log}
					wf := &web.Workflow{log}
					// request and dependencies that don't rely on request
					// get hoisted up.
					actual := genweb.Load(log, pg, wf, request)
					fmt.Fprintf(os.Stdout, "%s\n", valast.String(actual))
				}
			`,
			"web/web.go": `
				package web

				var env = ""

				func NewEnv() *Env {
					env += "env"
					return &Env{env}
				}

				type Env struct {
					value string
				}

				func NewLog(env *Env) *Log {
					return &Log{"log", env}
				}

				type Log struct {
					value string
					*Env
				}

				type Postgres struct {
					Log *Log
				}

				type Request struct {}

				type Workflow struct {
					Log *Log
				}

				type Session struct {
					*Request
					DB *Postgres
				}

				// Web struct
				type Web struct {
					*Session
					*Log
					*Workflow
				}
			`,
		},
	})
}

func TestInterfaceExternal(t *testing.T) {
	runTest(t, Test{
		Function: &di.Function{
			Name:   "Load",
			Target: "app.com/gen/web",
			Params: []*di.Param{
				{Import: "app.com/log", Type: "Log"},
			},
			Results: []di.Dependency{
				di.ToType("app.com/web", "*Web"),
			},
		},
		Expect: `
			&web.Web{Log: &log.log{}}
		`,
		Files: map[string]string{
			"go.mod": goMod,
			"main.go": `
				package main

				import (
					"os"
					"fmt"
					"github.com/hexops/valast"
					"app.com/log"
					genweb "app.com/gen/web"
				)

				func main() {
					log := log.Default()
					actual := genweb.Load(log)
					fmt.Fprintf(os.Stdout, "%s\n", valast.String(actual))
				}
			`,
			"log/log.go": `
				package log

				type Log interface {
					Info(s string)
				}

				type log struct {}

				func (l *log) Info(s string) {}

				func Default() Log {
					return &log{}
				}
			`,
			"web/web.go": `
				package web

				import (
					"app.com/log"
				)

				type Web struct {
					log.Log
				}
			`,
		},
	})
}

func TestInterfaceInput(t *testing.T) {
	runTest(t, Test{
		Function: &di.Function{
			Name:   "Load",
			Target: "app.com/gen/web",
			Results: []di.Dependency{
				di.ToType("app.com/web", "Log"),
			},
		},
		Expect: `
			&web.log{d: "default"}
		`,
		Files: map[string]string{
			"go.mod":  goMod,
			"main.go": mainGo,
			"web/web.go": `
				package web

				type Log interface {
					Info(s string)
				}

				type log struct {
					d string
				}

				func (l *log) Info(s string) {}

				func Default() Log {
					return &log{"default"}
				}
			`,
		},
	})
}

func TestInterface(t *testing.T) {
	runTest(t, Test{
		Function: &di.Function{
			Name:   "Load",
			Target: "app.com/gen/web",
			Results: []di.Dependency{
				di.ToType("app.com/web", "*Web"),
			},
		},
		Expect: `
			&web.Web{Log: &log.log{
				w: "stderr",
			}}
		`,
		Files: map[string]string{
			"go.mod":  goMod,
			"main.go": mainGo,
			"log/log.go": `
				package log

				type Log interface {
					Info(s string)
				}

				type log struct {
					w string
				}

				func (l *log) Info(s string) {}

				func Default() Log {
					return &log{"stderr"}
				}
			`,
			"web/web.go": `
				package web

				import (
					"app.com/log"
				)

				type Web struct {
					log.Log
				}
			`,
		},
	})
}

func TestPointers(t *testing.T) {
	runTest(t, Test{
		Function: &di.Function{
			Name:   "Load",
			Target: "app.com/gen/web",
			Results: []di.Dependency{
				di.ToType("app.com/web", "*Web"),
			},
		},
		Expect: `
			&web.Web{
				m: &web.Middleware{
					v: "v",
				},
				n: web.Middleware{v: "v"},
			}
		`,
		Files: map[string]string{
			"go.mod":  goMod,
			"main.go": mainGo,
			"web/web.go": `
				package web

				// Middlewares function
				func Middlewares() *Middleware {
					return &Middleware{"v"}
				}

				// Middleware handler
				type Middleware struct{
					v string
				}

				// New web
				func New(m *Middleware, n Middleware) *Web {
					return &Web{m, n}
				}

				// Web struct
				type Web struct {
					m *Middleware
					n Middleware
				}
			`,
		},
	})
}

func TestSameDataType(t *testing.T) {
	runTest(t, Test{
		Function: &di.Function{
			Name:   "Load",
			Target: "app.com/gen/web",
			Results: []di.Dependency{
				di.ToType("app.com/web", "*Web"),
			},
		},
		Expect: `
			&web.Web{w: &web.Web{
				bud: true,
			}}
		`,
		Files: map[string]string{
			"go.mod":  goMod,
			"main.go": mainGo,
			"bud/web/web.go": `
				package web

				// New web
				func New() *Web {
					return &Web{true}
				}

				// Web struct
				type Web struct {
					bud bool
				}
			`,
			"web/web.go": `
				package web

				import (
					"app.com/bud/web"
				)

				// New web
				func New(w *web.Web) *Web {
					return &Web{w}
				}

				// Web struct
				type Web struct {
					w *web.Web
				}
			`,
		},
	})
}

func TestSamePackage(t *testing.T) {
	runTest(t, Test{
		Function: &di.Function{
			Name:   "Load",
			Target: "app.com/gen/web",
			Results: []di.Dependency{
				di.ToType("app.com/web", "*Web"),
			},
		},
		Expect: `
			&web.Web{m: &web.Middleware{
				v: "v",
			}}
		`,
		Files: map[string]string{
			"go.mod":  goMod,
			"main.go": mainGo,
			"web/web.go": `
				package web

				// Middlewares function
				func Middlewares() *Middleware {
					return &Middleware{"v"}
				}

				// Middleware handler
				type Middleware struct{
					v string
				}

				// New web
				func New(m *Middleware) *Web {
					return &Web{m}
				}

				// Web struct
				type Web struct {
					m *Middleware
				}
			`,
		},
	})
}

func TestSameTarget(t *testing.T) {
	runTest(t, Test{
		Function: &di.Function{
			Name:   "Load",
			Target: "app.com/web",
			Results: []di.Dependency{
				di.ToType("app.com/web", "*Web"),
			},
		},
		Expect: `
			&web.Web{m: &web.Middleware{
				v: "v",
			}}
		`,
		Files: map[string]string{
			"go.mod": goMod,
			"main.go": `
				package main

				import (
					"os"
					"fmt"
					"github.com/hexops/valast"
					"app.com/web"
				)

				func main() {
					actual := web.Load()
					fmt.Fprintf(os.Stdout, "%s\n", valast.String(actual))
				}
			`,
			"web/web.go": `
				package web

				// Middlewares function
				func Middlewares() *Middleware {
					return &Middleware{"v"}
				}

				// Middleware handler
				type Middleware struct{
					v string
				}

				// New web
				func New(m *Middleware) *Web {
					return &Web{m}
				}

				// Web struct
				type Web struct {
					m *Middleware
				}
			`,
		},
	})
}

func TestSlice(t *testing.T) {
	runTest(t, Test{
		Function: &di.Function{
			Name:   "Load",
			Target: "app.com/gen/web",
			Results: []di.Dependency{
				di.ToType("app.com/web", "*Web"),
			},
		},
		Expect: `
			&web.Web{
				Logs:   []*log.Log{},
				Logger: &web.Logger{logs: []*log.Log{}},
			}
		`,
		Files: map[string]string{
			"go.mod":  goMod,
			"main.go": mainGo,
			"log/log.go": `
				package log

				func New() []*Log {
					return []*Log{}
				}

				type Log struct {}
			`,
			"web/web.go": `
				package web

				import (
					"app.com/log"
				)

				func New(logs []*log.Log) *Logger {
					return &Logger{logs}
				}

				type Logger struct {
					logs []*log.Log
				}

				type Web struct {
					Logs []*log.Log
					*Logger
				}
			`,
		},
	})
}

func TestStructMap(t *testing.T) {
	runTest(t, Test{
		Function: &di.Function{
			Name:   "Load",
			Target: "app.com/gen/web",
			Results: []di.Dependency{
				di.ToType("app.com/web", "*Web"),
			},
			Aliases: di.Aliases{
				di.ToType("app.com/js", "VM"): di.ToType("app.com/js/v8", "*V8"),
			},
		},
		Expect: `
			&web.Web{VM: &v8.V8{}}
		`,
		Files: map[string]string{
			"go.mod":  goMod,
			"main.go": mainGo,
			"web/web.go": `
				package web

				import (
					"app.com/js"
				)

				type Web struct {
					VM js.VM
				}
			`,
			"js/js.go": `
				package js

				type VM interface {
					Eval(input string) (string, error)
				}
			`,
			"js/v8/v8.go": `
				package v8

				type V8 struct {}

				func (v *V8) Eval(input string) (string, error) {
					return "", nil
				}
			`,
		},
	})
}

func TestFunctionMap(t *testing.T) {
	runTest(t, Test{
		Function: &di.Function{
			Name:   "Load",
			Target: "app.com/gen/web",
			Results: []di.Dependency{
				di.ToType("app.com/web", "*Web"),
			},
			Aliases: di.Aliases{
				di.ToType("app.com/js", "VM"): di.ToType("app.com/js/v8", "*V8"),
			},
		},
		Expect: `
			&web.Web{VM: &v8.V8{}}
		`,
		Files: map[string]string{
			"go.mod":  goMod,
			"main.go": mainGo,
			"web/web.go": `
				package web

				import (
					"app.com/js"
				)

				func New(vm js.VM) *Web {
					return &Web{vm}
				}

				type Web struct {
					VM js.VM
				}
			`,
			"js/js.go": `
				package js

				type VM interface {
					Eval(input string) (string, error)
				}
			`,
			"js/v8/v8.go": `
				package v8

				type V8 struct {}

				func (v *V8) Eval(input string) (string, error) {
					return "", nil
				}
			`,
		},
	})
}

func TestStructMapNeedsPointer(t *testing.T) {
	runTest(t, Test{
		Function: &di.Function{
			Name:   "Load",
			Target: "app.com/gen/web",
			Results: []di.Dependency{
				di.ToType("app.com/web", "*Web"),
			},
			Aliases: di.Aliases{
				di.ToType("app.com/js", "VM"): di.ToType("app.com/js/v8", "*V8"),
			},
		},
		Expect: `
			&web.Web{VM: &v8.V8{}}
		`,
		Files: map[string]string{
			"go.mod":  goMod,
			"main.go": mainGo,
			"web/web.go": `
				package web

				import (
					"app.com/js"
				)

				type Web struct {
					VM js.VM
				}
			`,
			"js/js.go": `
				package js

				type VM interface {
					Eval(input string) (string, error)
				}
			`,
			"js/v8/v8.go": `
				package v8

				func New() V8 {
					return V8{}
				}

				type V8 struct {}

				func (v *V8) Eval(input string) (string, error) {
					return "", nil
				}
			`,
		},
	})
}

func TestFunctionAliasToStruct(t *testing.T) {
	runTest(t, Test{
		Function: &di.Function{
			Name:   "Load",
			Target: "app.com/gen/web",
			Results: []di.Dependency{
				di.ToType("app.com/web", "*Web"),
			},
			Aliases: di.Aliases{
				di.ToType("app.com/js", "VM"): di.ToType("app.com/js/v8", "*V8"),
			},
		},
		Expect: `
			&web.Web{VM: &v8.V8{}}
		`,
		Files: map[string]string{
			"go.mod":  goMod,
			"main.go": mainGo,
			"web/web.go": `
				package web

				import (
					"app.com/js"
				)

				func New(vm js.VM) *Web {
					return &Web{vm}
				}

				type Web struct {
					VM js.VM
				}
			`,
			"js/js.go": `
				package js

				type VM interface {
					Eval(input string) (string, error)
				}
			`,
			"js/v8/v8.go": `
				package v8

				func New() V8 {
					return V8{}
				}

				type V8 struct {}

				func (v *V8) Eval(input string) (string, error) {
					return "", nil
				}
			`,
		},
	})
}

func TestFunctionInterfaceParam(t *testing.T) {
	runTest(t, Test{
		Function: &di.Function{
			Name:   "Load",
			Target: "app.com/gen/web",
			Params: []*di.Param{
				&di.Param{
					Import: "app.com/js/v8",
					Type:   "V8",
				},
			},
			Results: []di.Dependency{
				di.ToType("app.com/web", "*Web"),
			},
			Aliases: di.Aliases{
				di.ToType("app.com/js", "VM"): di.ToType("app.com/js/v8", "V8"),
			},
		},
		Expect: `
			&web.Web{VM: &v8.v8{}}
		`,
		Files: map[string]string{
			"go.mod": goMod,
			"main.go": `package main

				import (
					"os"
					"fmt"
					"github.com/hexops/valast"
					"app.com/gen/web"
					"app.com/js/v8"
				)

				func main() {
					v8 := v8.New()
					actual := web.Load(v8)
					fmt.Fprintf(os.Stdout, "%s\n", valast.String(actual))
				}
			`,
			"web/web.go": `
				package web

				import (
					"app.com/js"
				)

				func New(vm js.VM) *Web {
					return &Web{vm}
				}

				type Web struct {
					VM js.VM
				}
			`,
			"js/js.go": `
				package js

				type VM interface {
					Eval(input string) (string, error)
				}
			`,
			"js/v8/v8.go": `
				package v8

				func New() V8 {
					return &v8{}
				}

				type V8 interface {
					Eval(input string) (string, error)
				}

				type v8 struct {}

				func (v *v8) Eval(input string) (string, error) {
					return "", nil
				}
			`,
		},
	})
}

func TestInputStruct(t *testing.T) {
	runTest(t, Test{
		Function: &di.Function{
			Name:   "Load",
			Target: "app.com/gen/web",
			Results: []di.Dependency{
				&di.Struct{
					Import: "app.com/gen/web",
					Type:   "Web",
					Fields: []*di.StructField{
						{
							Name:   "A",
							Import: "app.com/web",
							Type:   "*A",
						},
						{
							Name:   "B",
							Import: "app.com/web",
							Type:   "B",
						},
					},
				},
			},
		},
		Expect: `
			web.Web{
				A: &web.A{},
				B: web.B{b: "b"},
			}
		`,
		Files: map[string]string{
			"go.mod":  goMod,
			"main.go": mainGo,
			"gen/web/web.go": `
				package web

				import (
					"app.com/web"
				)

				type Web struct {
					*web.A
					B web.B
				}
			`,
			"web/web.go": `
				package web

				type A struct {
				}

				func New() B {
					return B{"b"}
				}

				type B struct {
					b string
				}
			`,
		},
	})
}

func TestInputStructPointer(t *testing.T) {
	runTest(t, Test{
		Function: &di.Function{
			Name:   "Load",
			Target: "app.com/gen/web",
			Results: []di.Dependency{
				&di.Struct{
					Import: "app.com/gen/web",
					Type:   "*Web",
					Fields: []*di.StructField{
						{
							Name:   "A",
							Import: "app.com/web",
							Type:   "*A",
						},
						{
							Name:   "B",
							Import: "app.com/web",
							Type:   "B",
						},
					},
				},
			},
		},
		Expect: `
			&web.Web{
				A: &web.A{},
				B: web.B{b: "b"},
			}
		`,
		Files: map[string]string{
			"go.mod":  goMod,
			"main.go": mainGo,
			"gen/web/web.go": `
				package web

				import (
					"app.com/web"
				)

				type Web struct {
					*web.A
					B web.B
				}
			`,
			"web/web.go": `
				package web

				type A struct {
				}

				func New() B {
					return B{"b"}
				}

				type B struct {
					b string
				}
			`,
		},
	})
}

func TestErrorResultNoError(t *testing.T) {
	runTest(t, Test{
		Function: &di.Function{
			Name:   "Load",
			Target: "app.com/gen/web",
			Results: []di.Dependency{
				di.ToType("app.com/web", "*Web"),
				&di.Error{},
			},
		},
		Expect: `
			&web.Web{}
		`,
		Files: map[string]string{
			"go.mod":  goMod,
			"main.go": mainGoWithErr,
			"web/web.go": `
				package web

				// New web
				func New() *Web {
					return &Web{}
				}

				// Web struct
				type Web struct {
				}
			`,
		},
	})
}

func TestErrorResultWithError(t *testing.T) {
	runTest(t, Test{
		Function: &di.Function{
			Name:   "Load",
			Target: "app.com/gen/web",
			Results: []di.Dependency{
				di.ToType("app.com/web", "*Web"),
				&di.Error{},
			},
		},
		Expect: `
			unable to create web
		`,
		Files: map[string]string{
			"go.mod":  goMod,
			"main.go": mainGoWithErr,
			"web/web.go": `
				package web

				import "errors"

				// New web
				func New() (*Web, error) {
					return &Web{}, errors.New("unable to create web")
				}

				// Web struct
				type Web struct {
				}
			`,
		},
	})
}

func TestMappedExternal(t *testing.T) {
	runTest(t, Test{
		Function: &di.Function{
			Name:   "Load",
			Target: "app.com/gen/web",
			Params: []*di.Param{
				{Import: "app.com/gen", Type: "*FileSystem"},
			},
			Results: []di.Dependency{
				di.ToType("app.com/web", "*Web"),
			},
			Aliases: di.Aliases{
				di.ToType("app.com/gen", "FS"): di.ToType("app.com/gen", "*FileSystem"),
			},
		},
		Expect: `
			&web.Web{genfs: &gen.FileSystem{
				fsys: os.dirFS("."),
			}}
		`,
		Files: map[string]string{
			"go.mod": goMod,
			"main.go": `
				package main

				import (
					"os"
					"fmt"
					"github.com/hexops/valast"
					gen "app.com/gen"
					genweb "app.com/gen/web"
				)

				func main() {
					fsys := os.DirFS(".")
					genfs := gen.New(fsys)
					actual := genweb.Load(genfs)
					fmt.Fprintf(os.Stdout, "%s\n", valast.String(actual))
				}
			`,
			"web/web.go": `
				package web

				import "app.com/gen"

				// New web
				func New(genfs gen.FS) (*Web) {
					return &Web{genfs}
				}

				// Web struct
				type Web struct {
					genfs gen.FS
				}
			`,
			"gen/gen.go": `
				package gen

				import "io/fs"

				type FS interface{}

				type FileSystem struct {
					fsys fs.FS
				}

				func New(fsys fs.FS) *FileSystem {
					return &FileSystem{fsys}
				}
			`,
		},
	})
}

func TestHoistEmpty(t *testing.T) {
	runTest(t, Test{
		Function: &di.Function{
			Hoist:  true,
			Name:   "Load",
			Target: "app.com/gen/web",
			Params: []*di.Param{
				{Import: "net/http", Type: "ResponseWriter"},
				{Import: "net/http", Type: "*Request"},
			},
			Results: []di.Dependency{
				di.ToType("app.com/web", "*Controller"),
			},
		},
		Expect: `
			&web.Controller{}
		`,
		Files: map[string]string{
			"go.mod":  goMod,
			"main.go": mainGo,
			"web/web.go": `
				package web

				type Controller struct {}
			`,
		},
	})
}

func TestSkipMethods(t *testing.T) {
	runTest(t, Test{
		Function: &di.Function{
			Name:   "Load",
			Target: "app.com/gen/web",
			Results: []di.Dependency{
				di.ToType("app.com/parser", "*Parser"),
			},
		},
		Expect: `
			&parser.Parser{message: "new"}
		`,
		Files: map[string]string{
			"go.mod":  goMod,
			"main.go": mainGo,
			"parser/parser.go": `
				package parser

				type Package struct {}

				func (p *Package) Parser() *Parser {
					return &Parser{"package"}
				}

				func New() *Parser {
					return &Parser{"new"}
				}

				type Parser struct {
					message string
				}

			`,
		},
	})
}

func TestDuplicates(t *testing.T) {
	runTest(t, Test{
		Function: &di.Function{
			Name:   "Load",
			Target: "app.com/gen/web",
			Results: []di.Dependency{
				di.ToType("app.com/controller", "*Controller"),
			},
		},
		Expect: `
		&controller.Controller{
			C1: &comments.Comment{
				One: 1,
			},
			C2: &comments.Comment{Two: 2},
		}
		`,
		Files: map[string]string{
			"go.mod":  goMod,
			"main.go": mainGo,
			"controller/controller.go": `
				package controller
				import (
					comments "app.com/comments"
					comments1 "app.com/posts/comments"
				)
				type Controller struct {
					C1 *comments.Comment
					C2 *comments1.Comment
				}
				func New(c1 *comments.Comment, c2 *comments1.Comment) *Controller {
					return &Controller{c1, c2}
				}
			`,
			"comments/comments.go": `
				package comments
				func New() *Comment { return &Comment{1} }
				type Comment struct { One int }
			`,
			"posts/comments/comments.go": `
				package comments
				func New() *Comment { return &Comment{2} }
				type Comment struct { Two int }
			`,
		},
	})
}

func TestHoistable(t *testing.T) {
	runTest(t, Test{
		Function: &di.Function{
			Name:   "Load",
			Hoist:  true,
			Target: "app.com/gen/web",
			Params: []*di.Param{
				{Import: "context", Type: "Context", Hoist: true},
				{Import: "app.com/web", Type: "*Request"},
			},
			Results: []di.Dependency{
				di.ToType("app.com/web", "*Web"),
				&di.Error{},
			},
		},
		Expect: `
			&web.Web{
				Session: &web.Session{
					DB: &web.DB{
						URL:    "url",
						Loaded: 1,
					},
					Request: &web.Request{Path: "/"},
				},
				DB: &web.DB{
					URL:    "url",
					Loaded: 1,
				},
			}
		`,
		Files: map[string]string{
			"go.mod": goMod,
			"main.go": `
				package main

				import (
					"os"
					"fmt"
					"context"
					"github.com/hexops/valast"
					web "app.com/web"
					genweb "app.com/gen/web"
				)

				func main() {
					ctx := context.Background()
					db, err := web.LoadDB(ctx)
					if err != nil {
						fmt.Fprintf(os.Stderr, "%s\n", err.Error())
						return
					}
					req := web.NewRequest()
					actual, err := genweb.Load(db, req)
					if err != nil {
						fmt.Fprintf(os.Stderr, "%s\n", err.Error())
						return
					}
					fmt.Fprintf(os.Stdout, "%s\n", valast.String(actual))
				}
			`,
			"web/web.go": `
				package web
				import "context"
				var loaded = 0
				func LoadDB(ctx context.Context) (*DB, error) {
					loaded++
					return &DB{"url", loaded}, nil
				}
				type DB struct {
					URL string
					Loaded int
				}
				func NewRequest() *Request {
					return &Request{Path: "/"}
				}
				type Request struct {
					Path string
				}
				type Session struct {
					DB *DB
					Request *Request
				}
				type Web struct {
					Session *Session
					DB *DB
				}
			`,
		},
	})
}

func TestV8Alias(t *testing.T) {
	runTest(t, Test{
		Function: &di.Function{
			Name:   "Load",
			Target: "app.com/gen/web",
			Params: []*di.Param{},
			Results: []di.Dependency{
				di.ToType("app.com/web", "*Controller"),
				&di.Error{},
			},
			Aliases: di.Aliases{
				di.ToType("app.com/js", "VM"): di.ToType("app.com/js/v8", "*VM"),
			},
		},
		Expect: `
			&web.Controller{VM: &js.VM{}}
		`,
		Files: map[string]string{
			"go.mod":  goMod,
			"main.go": mainGoWithErr,
			"js/vm.go": `
				package js
				type VM interface {
					Eval(expr string) (string, error)
				}
			`,
			"js/v8/v8.go": `
				package js
				func Load() (*VM, error) {
					return &VM{}, nil
				}
				type VM struct {}
				func (v *VM) Eval(expr string) (string, error) {
					return "v8", nil
				}
			`,
			"web/web.go": `
				package web
				import "app.com/js"
				func Load(vm js.VM) *Controller {
					return &Controller{vm}
				}
				type Controller struct {
					VM js.VM
				}
			`,
		},
	})
}

func TestAliasBack(t *testing.T) {
	runTest(t, Test{
		Function: &di.Function{
			Name:   "Load",
			Target: "app.com/gen/web",
			Params: []*di.Param{},
			Results: []di.Dependency{
				di.ToType("app.com/web", "*Controller"),
				&di.Error{},
			},
			Aliases: di.Aliases{
				di.ToType("app.com/log", "Log"): di.ToType("app.com/log/console", "Log"),
			},
		},
		Expect: `
			&web.Controller{log: &console.console{}}
		`,
		Files: map[string]string{
			"go.mod":  goMod,
			"main.go": mainGoWithErr,
			"log/log.go": `
				package log
				type Log interface {
					Log(msg string)
				}
			`,
			"log/console/console.go": `
				package console
				import "app.com/log"
				func Load() (Log, error) {
					return &console{}, nil
				}
				type console struct{}
				func (c *console) Log(msg string) {}
				type Log = log.Log
			`,
			"web/web.go": `
				package web
				import "app.com/log"
				func Load(log log.Log) *Controller {
					return &Controller{log}
				}
				type Controller struct {
					log log.Log
				}
			`,
		},
	})
}

func TestRuntimeAliasTo3rdPartyLibrary(t *testing.T) {
	runTest(t, Test{
		Function: &di.Function{
			Name:   "Load",
			Target: "app.com/gen/web",
			Params: []*di.Param{},
			Results: []di.Dependency{
				di.ToType("app.com/web", "*Generator"),
			},
			Aliases: di.Aliases{},
		},
		Expect: `
			&web.Generator{tr: &transpiler.Transpiler{
				ids:   map[string]int{},
				exts:  map[int]string{},
				fns:   map[string][]func(*transpiler.File) error{},
				graph: &dijkstra.Graph{mapping: map[string]int{}},
			}}
		`,
		Files: map[string]string{
			"go.mod": `
				module app.com
				go 1.17
				require (
					github.com/hexops/valast v1.4.1
					github.com/livebud/transpiler v0.0.3
				)
			`,
			"main.go": mainGo,
			"web/web.go": `
				package web
				import "app.com/runtime/transpiler"
				func Load(tr *transpiler.Transpiler) *Generator {
					return &Generator{tr}
				}
				type Generator struct {
					tr *transpiler.Transpiler
				}
			`,
			"runtime/transpiler/transpiler.go": `
				package transpiler
				import "github.com/livebud/transpiler"
				type Transpiler = transpiler.Transpiler
			`,
		},
	})
}

func TestRuntimeAliasToLibraryFunction(t *testing.T) {
	runTest(t, Test{
		Function: &di.Function{
			Name:   "Load",
			Target: "app.com/gen/web",
			Params: []*di.Param{},
			Results: []di.Dependency{
				di.ToType("app.com/web", "*Generator"),
			},
			Aliases: di.Aliases{},
		},
		Expect: `&web.Generator{tr: &transpiler.Transpiler{}}`,
		Files: map[string]string{
			"go.mod":  goMod,
			"main.go": mainGo,
			"web/web.go": `
				package web
				import "app.com/runtime/transpiler"
				func Load(tr *transpiler.Transpiler) *Generator {
					return &Generator{tr}
				}
				type Generator struct {
					tr *transpiler.Transpiler
				}
			`,
			"runtime/transpiler/transpiler.go": `
				package transpiler
				import "app.com/library/transpiler"
				type Transpiler = transpiler.Transpiler
			`,
			"library/transpiler/transpiler.go": `
				package transpiler
				func New() *Transpiler {
					return &Transpiler{}
				}
				type Transpiler struct {}
			`,
		},
	})
}

func TestRuntimeAliasToLibraryStruct(t *testing.T) {
	runTest(t, Test{
		Function: &di.Function{
			Name:   "Load",
			Target: "app.com/gen/web",
			Params: []*di.Param{},
			Results: []di.Dependency{
				di.ToType("app.com/web", "*Generator"),
			},
			Aliases: di.Aliases{},
		},
		Expect: `&web.Generator{Transpiler: &transpiler.Transpiler{}}`,
		Files: map[string]string{
			"go.mod":  goMod,
			"main.go": mainGo,
			"web/web.go": `
				package web
				import "app.com/runtime/transpiler"
				type Generator struct {
					*transpiler.Transpiler
				}
			`,
			"runtime/transpiler/transpiler.go": `
				package transpiler
				import "app.com/library/transpiler"
				type Transpiler = transpiler.Transpiler
			`,
			"library/transpiler/transpiler.go": `
				package transpiler
				func New() *Transpiler {
					return &Transpiler{}
				}
				type Transpiler struct {}
			`,
		},
	})
}

func TestHTTPRequest(t *testing.T) {
	runTest(t, Test{
		Function: &di.Function{
			Name:   "Load",
			Target: "app.com/gen/web",
			Params: []*di.Param{
				{Import: "context", Type: "Context", Hoist: true},
				{Import: "net/http", Type: "*Request"},
				{Import: "net/http", Type: "ResponseWriter"},
			},
			Results: []di.Dependency{
				di.ToType("app.com/web", "*Controller"),
			},
			Aliases: di.Aliases{},
		},
		Expect: `
			&web.Controller{Pool: &postgres.Pool{
				Path: "/foo",
			}}
		`,
		Files: map[string]string{
			"go.mod": goMod,
			"main.go": `
				package main
				import (
					"os"
					"fmt"
					"github.com/hexops/valast"
					"net/http/httptest"
					"app.com/gen/web"
				)
				func main() {
					req := httptest.NewRequest("GET", "http://example.com/foo", nil)
					actual := web.Load(req)
					fmt.Fprintf(os.Stdout, "%s\n", valast.String(actual))
				}
			`,
			"web/web.go": `
				package web
				import "app.com/postgres"
				type Controller struct {
					Pool *postgres.Pool
				}
			`,
			"postgres/pool.go": `
				package postgres
				import "net/http"
				func New(r *http.Request) *Pool { return &Pool{r.URL.Path} }
				type Pool struct { Path string }
			`,
		},
	})
}

func TestMapParamAlias(t *testing.T) {
	t.Skip("aliasing to a map is not implemented yet")
	runTest(t, Test{
		Function: &di.Function{
			Name:   "Load",
			Target: "app.com/gen/web",
			Params: []*di.Param{},
			Results: []di.Dependency{
				di.ToType("app.com/web", "*Controller"),
			},
			Aliases: di.Aliases{
				di.ToType("app.com/viewer", "Pages"): di.ToType("app.com/viewer/svelte", "Pages"),
			},
		},
		Expect: `
			&web.Controller{Pool: &postgres.Pool{
				Path: "/foo",
			}}
		`,
		Files: map[string]string{
			"go.mod":  goMod,
			"main.go": mainGo,
			"web/web.go": `
				package web
				import "app.com/viewer/svelte"
				type Controller struct {
					Svelte *svelte.Viewer
				}
			`,
			"viewer/viewer.go": `
				package viewer
				type Pages = map[string]string
			`,
			"viewer/svelte/svelte.go": `
				package svelte
				import "app.com/viewer"
				func New(pages Pages) *Viewer {
					return &Viewer{pages}
				}
				type Pages = viewer.Pages
				func NewPages() Pages {
					return viewer.Pages{}
				}
				type Viewer struct {
					pages Pages
				}
			`,
		},
	})
}

func TestTypeSpec(t *testing.T) {
	runTest(t, Test{
		Function: &di.Function{
			Name:   "Load",
			Target: "app.com/gen/web",
			Params: []*di.Param{},
			Results: []di.Dependency{
				di.ToType("app.com/web", "*Controller"),
			},
			Aliases: di.Aliases{},
		},
		Expect: `
			&web.Controller{
				Actions: web.Actions{
					"hello": "world",
				},
				URL: web.URL("hello"),
			}
		`,
		Files: map[string]string{
			"go.mod":  goMod,
			"main.go": mainGo,
			"web/web.go": `
				package web
				func NewActions() Actions {
					return Actions{"hello": "world"}
				}
				type Actions map[string]string
				// type Actions2 map[string]string
				func NewURL() URL {
					return URL("hello")
				}
				type URL string
				type Controller struct {
					Actions Actions
					URL URL
				}
			`,
		},
	})
}

func TestInterfaceErrorUnclear(t *testing.T) {
	runTest(t, Test{
		Function: &di.Function{
			Name:   "Load",
			Target: "app.com/gen/web",
			Params: []*di.Param{},
			Results: []di.Dependency{
				di.ToType("app.com/web", "*Controller"),
			},
			Aliases: di.Aliases{},
		},
		Expect: `di: unclear how to provide 'app.com/js'.VM`,
		Files: map[string]string{
			"go.mod":  goMod,
			"main.go": mainGo,
			"js/js.go": `
				package js
				type VM interface {
					Eval(expr string) (string, error)
				}
			`,
			"web/web.go": `
				package web
				import "app.com/js"
				type Controller struct {
					VM js.VM
				}
			`,
		},
	})
}

// TODO: figure out how to test imports as inputs

// IDEA: consider renaming Target to Import
// IDEA: consider moving Hoist outside of Function
// IDEA: consider transitioning to a builder pattern input
