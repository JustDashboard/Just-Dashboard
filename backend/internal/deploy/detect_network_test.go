package deploy

import (
	"context"
	"slices"
	"strings"
	"testing"
)

// Detection end to end: a repository's files in, the candidate's port,
// profile, start command and listen facts out, the way the configure form
// and preflight receive them.

func detectCandidates(t *testing.T, files map[string]string) []DetectedCandidate {
	t.Helper()
	root := t.TempDir()
	for path, content := range files {
		writeBuildFixture(t, root, path, content)
	}
	result, err := (Detector{}).DetectPath(context.Background(), root, SourceIdentity{Kind: SourceLocal})
	if err != nil {
		t.Fatal(err)
	}
	if err := validateDetectionResult(&DraftSourceConfig{Kind: SourceLocal}, withSelection(result)); err != nil {
		t.Fatalf("detection does not validate: %v\n%+v", err, result)
	}
	return result.Candidates
}

// withSelection selects the first candidate so the stored-detection
// validation, which requires a selection, can run on any fixture.
func withSelection(result DetectionResult) DetectionResult {
	if result.SelectedID == "" && len(result.Candidates) > 0 {
		result.SelectedID = result.Candidates[0].ID
	}
	return result
}

func candidateBy(t *testing.T, candidates []DetectedCandidate, method BuildMethod, recipe string) DetectedCandidate {
	t.Helper()
	for _, candidate := range candidates {
		if candidate.BuildMethod == method && candidate.Recipe == recipe {
			return candidate
		}
	}
	t.Fatalf("no %s %s candidate in %+v", method, recipe, candidates)
	return DetectedCandidate{}
}

func listenOf(candidate DetectedCandidate) DetectedListen {
	if candidate.Listen == nil {
		return DetectedListen{}
	}
	return *candidate.Listen
}

func TestNodeListenFactsComeFromScriptsAndCode(t *testing.T) {
	t.Parallel()
	lock := map[string]string{"package-lock.json": "{}"}
	with := func(files map[string]string) map[string]string {
		for name, content := range lock {
			files[name] = content
		}
		return files
	}
	for _, test := range []struct {
		name            string
		files           map[string]string
		port            int
		fixed           int
		readsPort       bool
		loopback        string
		certain         bool
		recipeFix       string
		start           string
		loopbackFromHas string
	}{
		{
			name:  "express listening on a literal",
			files: with(map[string]string{"package.json": `{"scripts":{"start":"node server.js"},"dependencies":{"express":"4"}}`, "server.js": "const app = require('express')()\napp.listen(4000)\n"}),
			port:  4000, fixed: 4000,
		},
		{
			name:  "express reading PORT with a fallback",
			files: with(map[string]string{"package.json": `{"scripts":{"start":"node index.js"},"dependencies":{"express":"4"}}`, "index.js": "app.listen(process.env.PORT || 5000)\n"}),
			port:  5000, readsPort: true,
		},
		{
			name:  "next start with a port flag",
			files: with(map[string]string{"package.json": `{"scripts":{"build":"next build","start":"next start -p 3001"},"dependencies":{"next":"16"}}`}),
			port:  3001, fixed: 3001,
		},
		{
			name:  "PORT prefix in the start script",
			files: with(map[string]string{"package.json": `{"scripts":{"start":"PORT=4000 node server.js"},"dependencies":{"koa":"2"}}`, "server.js": "app.listen(process.env.PORT)\n"}),
			port:  4000, fixed: 4000,
		},
		{
			name:  "explicit loopback in code",
			files: with(map[string]string{"package.json": `{"scripts":{"start":"node server.js"},"dependencies":{"express":"4"}}`, "server.js": "app.listen(3000, '127.0.0.1')\n"}),
			port:  3000, fixed: 3000, loopback: "127.0.0.1", certain: true, loopbackFromHas: "server.js:1",
		},
		{
			// An admin listener on localhost beside the server on PORT is not
			// the port the proxy reaches, and cannot make the loopback certain.
			name:  "a loopback admin listener beside the server on PORT",
			files: with(map[string]string{"package.json": `{"scripts":{"start":"node server.js"},"dependencies":{"express":"4"}}`, "server.js": "admin.listen(9229, '127.0.0.1')\napp.listen(process.env.PORT)\n"}),
			port:  3000, readsPort: true, loopback: "127.0.0.1",
		},
		{
			name:  "fastify without a host",
			files: with(map[string]string{"package.json": `{"scripts":{"start":"node app.js"},"dependencies":{"fastify":"5"}}`, "app.js": "import Fastify from 'fastify'\nconst app = Fastify()\nawait app.listen({ port: 3000 })\n"}),
			port:  3000, fixed: 3000, loopback: "localhost",
		},
		{
			name:  "HOST with a loopback fallback is moved by the recipe",
			files: with(map[string]string{"package.json": `{"scripts":{"start":"node server.js"},"dependencies":{"express":"4"}}`, "server.js": "const host = process.env.HOST || 'localhost'\napp.listen(process.env.PORT || 3000, host)\n"}),
			port:  3000, readsPort: true, loopback: "localhost", recipeFix: "HOST=0.0.0.0",
		},
		{
			name:  "next start bound to localhost",
			files: with(map[string]string{"package.json": `{"scripts":{"build":"next build","start":"next start -H localhost"},"dependencies":{"next":"16"}}`}),
			port:  3000, loopback: "localhost", certain: true,
		},
		{
			name:  "nest reads its port in src/main.ts",
			files: with(map[string]string{"package.json": `{"scripts":{"build":"nest build","start:prod":"node dist/main"},"dependencies":{"@nestjs/core":"11"}}`, "src/main.ts": "const app = await NestFactory.create(AppModule)\nawait app.listen(process.env.PORT ?? 3000)\n"}),
			port:  3000, readsPort: true,
		},
		{
			name:  "a framework's own server is not read as code",
			files: with(map[string]string{"package.json": `{"scripts":{"build":"next build","start":"next start"},"dependencies":{"next":"16"}}`, "server.js": "app.listen(9999, '127.0.0.1')\n"}),
			port:  3000,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			candidate := candidateBy(t, detectCandidates(t, test.files), BuildRecipe, "node")
			listen := listenOf(candidate)
			if candidate.Port != test.port || listen.Port != test.fixed || listen.ReadsPort != test.readsPort ||
				listen.Loopback != test.loopback || listen.LoopbackCertain != test.certain || listen.LoopbackRecipeFix != test.recipeFix ||
				!strings.Contains(listen.LoopbackFrom, test.loopbackFromHas) {
				t.Fatalf("candidate port %d, listen %+v\nevidence %+v", candidate.Port, listen, candidate.Evidence)
			}
			if test.start != "" && candidate.StartCommand != test.start {
				t.Fatalf("start = %q", candidate.StartCommand)
			}
		})
	}
}

func TestPreviewServersAreStartedOnEveryInterface(t *testing.T) {
	t.Parallel()
	candidates := detectCandidates(t, map[string]string{
		"package.json": `{"scripts":{"build":"tsc","start":"vite preview"},"dependencies":{"express":"4"}}`, "package-lock.json": "{}",
	})
	candidate := candidateBy(t, candidates, BuildRecipe, "node")
	listen := listenOf(candidate)
	if candidate.StartCommand != "npx vite preview --host 0.0.0.0" || candidate.Port != 4173 || listen.Port != 4173 || listen.Loopback != "" {
		t.Fatalf("candidate = %+v, listen %+v", candidate, listen)
	}
}

func TestAuthJSGetsTheVariablesItNeedsBehindTheProxy(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		version, name, value, template string
	}{
		{"5.0.0-beta.25", "AUTH_TRUST_HOST", "true", ""},
		{"beta", "AUTH_TRUST_HOST", "true", ""},
		{"^4.24.11", "NEXTAUTH_URL", "", "{{scheme}}://{{hostname}}"},
	} {
		candidates := detectCandidates(t, map[string]string{
			"package.json":      `{"scripts":{"build":"next build","start":"next start"},"dependencies":{"next":"16","next-auth":"` + test.version + `"}}`,
			"package-lock.json": "{}", "Dockerfile": "FROM node:22\nEXPOSE 3000\n",
		})
		for _, method := range []BuildMethod{BuildRecipe, BuildDockerfile} {
			recipe := "node"
			if method == BuildDockerfile {
				recipe = ""
			}
			candidate := candidateBy(t, candidates, method, recipe)
			if len(candidate.NetworkVariables) != 1 || candidate.NetworkVariables[0].Name != test.name ||
				candidate.NetworkVariables[0].Value != test.value || candidate.NetworkVariables[0].DomainTemplate != test.template {
				t.Fatalf("%s %s: %+v", test.version, method, candidate.NetworkVariables)
			}
		}
	}
	candidates := detectCandidates(t, map[string]string{
		"package.json": `{"scripts":{"build":"vite build"},"dependencies":{"@sveltejs/kit":"2","@sveltejs/adapter-node":"5","@auth/sveltekit":"1"}}`, "package-lock.json": "{}",
	})
	if variables := candidateBy(t, candidates, BuildRecipe, "node").NetworkVariables; len(variables) != 1 || variables[0].Name != "AUTH_TRUST_HOST" {
		t.Fatalf("@auth/sveltekit: %+v", variables)
	}
	candidates = detectCandidates(t, map[string]string{
		"package.json": `{"scripts":{"build":"vite build"},"dependencies":{"vite":"6","next-auth":"5.0.0"}}`, "package-lock.json": "{}",
	})
	if variables := candidateBy(t, candidates, BuildRecipe, "node").NetworkVariables; len(variables) != 0 {
		t.Fatalf("a static site got server variables: %+v", variables)
	}
}

func TestPythonListenFactsFollowTheServedCommand(t *testing.T) {
	t.Parallel()
	requirements := "fastapi\nuvicorn\n"
	for _, test := range []struct {
		name      string
		files     map[string]string
		port      int
		fixed     int
		readsPort bool
		loopback  string
		certain   bool
		recipeFix string
	}{
		{
			name:  "detected commands follow PORT",
			files: map[string]string{"requirements.txt": requirements, "main.py": "from fastapi import FastAPI\napp = FastAPI()\n"},
			port:  8000, readsPort: true,
		},
		{
			name:  "procfile port flag",
			files: map[string]string{"requirements.txt": requirements, "main.py": "app = FastAPI()\n", "Procfile": "web: uvicorn main:app --host 0.0.0.0 --port 5000\n"},
			port:  5000, fixed: 5000,
		},
		{
			name:  "procfile uvicorn without a host is moved by the recipe",
			files: map[string]string{"requirements.txt": requirements, "main.py": "app = FastAPI()\n", "Procfile": "web: uvicorn main:app --port $PORT\n"},
			port:  8000, readsPort: true, loopback: "127.0.0.1", recipeFix: "UVICORN_HOST=0.0.0.0",
		},
		{
			name:  "procfile gunicorn reads its config file",
			files: map[string]string{"requirements.txt": "flask\ngunicorn\n", "app.py": "app = Flask(__name__)\n", "Procfile": "web: gunicorn app:app\n", "gunicorn.conf.py": "bind = \"0.0.0.0:5000\"\n"},
			port:  5000, fixed: 5000,
		},
		{
			name:  "procfile gunicorn without a bind follows PORT",
			files: map[string]string{"requirements.txt": "flask\ngunicorn\n", "app.py": "app = Flask(__name__)\n", "Procfile": "web: gunicorn app:app\n"},
			port:  8000, readsPort: true,
		},
		{
			name:  "procfile runs flask's development server",
			files: map[string]string{"requirements.txt": "flask\n", "app.py": "from flask import Flask\napp = Flask(__name__)\nif __name__ == '__main__':\n    app.run(port=5000)\n", "Procfile": "web: python app.py\n"},
			port:  5000, fixed: 5000, loopback: "127.0.0.1", certain: true,
		},
		{
			// app.run ignores PORT and, given no port, listens on 5000.
			name:  "procfile runs app.run on every interface without a port",
			files: map[string]string{"requirements.txt": "flask\n", "app.py": "from flask import Flask\napp = Flask(__name__)\nif __name__ == '__main__':\n    app.run(host='0.0.0.0')\n", "Procfile": "web: python app.py\n"},
			port:  5000, fixed: 5000,
		},
		{
			name:  "procfile runs app.run with a positional host and port",
			files: map[string]string{"requirements.txt": "flask\n", "app.py": "from flask import Flask\napp = Flask(__name__)\napp.run('0.0.0.0', 8090, debug=False)\n", "Procfile": "web: python app.py\n"},
			port:  8090, fixed: 8090,
		},
		{
			name:  "procfile runs uvicorn.run without a port",
			files: map[string]string{"requirements.txt": requirements, "main.py": "import uvicorn\nfrom fastapi import FastAPI\napp = FastAPI()\nuvicorn.run(app, host='0.0.0.0')\n", "Procfile": "web: python main.py\n"},
			port:  8000, fixed: 8000,
		},
		{
			name:  "procfile runserver on its default",
			files: map[string]string{"requirements.txt": "django\n", "manage.py": "", "mysite/wsgi.py": "", "Procfile": "web: python manage.py runserver\n"},
			port:  8000, fixed: 8000, loopback: "127.0.0.1", certain: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			candidate := candidateBy(t, detectCandidates(t, test.files), BuildRecipe, "python")
			listen := listenOf(candidate)
			if candidate.Port != test.port || listen.Port != test.fixed || listen.ReadsPort != test.readsPort ||
				listen.Loopback != test.loopback || listen.LoopbackCertain != test.certain || listen.LoopbackRecipeFix != test.recipeFix {
				t.Fatalf("port %d, listen %+v, start %q", candidate.Port, listen, candidate.StartCommand)
			}
		})
	}
}

func TestGoServicesThatServeHTTPAreWebWithTheirPort(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		files     map[string]string
		profile   WorkloadProfile
		framework string
		port      int
		fixed     int
		readsPort bool
		loopback  string
		certain   bool
	}{
		{
			name:    "gin literal",
			files:   map[string]string{"go.mod": "module x\n\ngo 1.26\n\nrequire github.com/gin-gonic/gin v1.10.0\n", "main.go": "package main\n\nfunc main() {\n\tr := gin.Default()\n\tr.Run(\":8081\")\n}\n"},
			profile: ProfileWeb, framework: "gin", port: 8081, fixed: 8081,
		},
		{
			name:    "gin Run follows PORT",
			files:   map[string]string{"go.mod": "module x\n\ngo 1.26\n\nrequire github.com/gin-gonic/gin v1.10.0\n", "main.go": "package main\n\nfunc main() {\n\tr := gin.Default()\n\tr.Run()\n}\n"},
			profile: ProfileWeb, framework: "gin", port: 8080, readsPort: true,
		},
		{
			name:    "net/http literal",
			files:   map[string]string{"go.mod": "module x\n\ngo 1.26\n", "main.go": "package main\n\nfunc main() {\n\thttp.ListenAndServe(\":9000\", nil)\n}\n"},
			profile: ProfileWeb, framework: "go", port: 9000, fixed: 9000,
		},
		{
			name:    "net/http reading PORT",
			files:   map[string]string{"go.mod": "module x\n\ngo 1.26\n", "cmd/api/main.go": "package main\n\nfunc main() {\n\tport := os.Getenv(\"PORT\")\n\thttp.ListenAndServe(\":\"+port, nil)\n}\n"},
			profile: ProfileWeb, framework: "go", port: 8080, readsPort: true,
		},
		{
			name:    "echo on localhost",
			files:   map[string]string{"go.mod": "module x\n\ngo 1.26\n\nrequire github.com/labstack/echo/v4 v4.13.0\n", "main.go": "package main\n\nfunc main() {\n\te := echo.New()\n\te.Logger.Fatal(e.Start(\"localhost:1323\"))\n}\n"},
			profile: ProfileWeb, framework: "echo", port: 1323, fixed: 1323, loopback: "localhost", certain: true,
		},
		{
			name:    "a worker stays a service",
			files:   map[string]string{"go.mod": "module x\n\ngo 1.26\n", "main.go": "package main\n\nfunc main() { for { work() } }\n"},
			profile: ProfileService, framework: "go",
		},
		{
			// A Redis client's Addr is not a listener, and the server's
			// Addr built from PORT names no port of its own.
			name: "a client's Addr beside a server on PORT",
			files: map[string]string{"go.mod": "module x\n\ngo 1.26\n", "main.go": "package main\n\nfunc main() {\n\trdb := redis.NewClient(&redis.Options{Addr: \"localhost:6379\"})\n\t_ = rdb\n" +
				"\tport := os.Getenv(\"PORT\")\n\tsrv := &http.Server{Addr: \":\" + port, Handler: mux}\n\tlog.Fatal(srv.ListenAndServe())\n}\n"},
			profile: ProfileWeb, framework: "go", port: 8080, readsPort: true,
		},
		{
			// pprof's localhost:6060 is the debugging endpoint beside the
			// server; `":" + port` is no address literal.
			name: "a pprof listener beside a server on PORT",
			files: map[string]string{"go.mod": "module x\n\ngo 1.26\n", "main.go": "package main\n\nimport (\n\t\"net/http\"\n\t_ \"net/http/pprof\"\n\t\"os\"\n)\n\nfunc main() {\n" +
				"\tgo http.ListenAndServe(\"localhost:6060\", nil)\n\tport := os.Getenv(\"PORT\")\n\taddr := \":\" + port\n\thttp.ListenAndServe(addr, mux)\n}\n"},
			profile: ProfileWeb, framework: "go", port: 8080, readsPort: true,
		},
		{
			name:    "an admin listener on loopback beside a server the scan cannot read",
			files:   map[string]string{"go.mod": "module x\n\ngo 1.26\n", "main.go": "package main\n\nfunc main() {\n\tgo http.ListenAndServe(\"127.0.0.1:9090\", admin)\n\thttp.ListenAndServe(cfg.ListenAddr, mux)\n}\n"},
			profile: ProfileService, framework: "go", loopback: "127.0.0.1",
		},
		{
			name: "a server literal on loopback is certain",
			files: map[string]string{"go.mod": "module x\n\ngo 1.26\n", "main.go": "package main\n\nfunc main() {\n\tsrv := &http.Server{Addr: \"127.0.0.1:8085\", Handler: mux}\n\tsrv.ListenAndServe()\n}\n",
				"main_test.go": "package main\n\nfunc TestServe(t *testing.T) { go http.ListenAndServe(\":0\", nil) }\n"},
			profile: ProfileWeb, framework: "go", port: 8085, fixed: 8085, loopback: "127.0.0.1", certain: true,
		},
		{
			// A worker that only exposes Prometheus metrics is not a web
			// server: GET / would 404 on it.
			name:    "a metrics-only worker stays a service",
			files:   map[string]string{"go.mod": "module x\n\ngo 1.26\n", "main.go": "package main\n\nfunc main() {\n\thttp.Handle(\"/metrics\", promhttp.Handler())\n\tgo http.ListenAndServe(\":2112\", nil)\n\tfor { work() }\n}\n"},
			profile: ProfileService, framework: "go", port: 2112, fixed: 2112,
		},
		{
			name:    "grpc keeps its port without becoming web",
			files:   map[string]string{"go.mod": "module x\n\ngo 1.26\n\nrequire google.golang.org/grpc v1.70.0\n", "main.go": "package main\n\nfunc main() {\n\tlis, _ := net.Listen(\"tcp\", \":50051\")\n\t_ = lis\n}\n"},
			profile: ProfileService, framework: "go", port: 50051, fixed: 50051,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			candidate := candidateBy(t, detectCandidates(t, test.files), BuildRecipe, "go")
			listen := listenOf(candidate)
			if candidate.Profile != test.profile || candidate.Framework != test.framework || candidate.Port != test.port ||
				listen.Port != test.fixed || listen.ReadsPort != test.readsPort || listen.Loopback != test.loopback ||
				listen.LoopbackCertain != test.certain {
				t.Fatalf("candidate %s %s port %d, listen %+v", candidate.Profile, candidate.Framework, candidate.Port, listen)
			}
		})
	}
}

func TestRustBindsAndPortsDropTheConfirmationWhenTheSourceAnswers(t *testing.T) {
	t.Parallel()
	confirm := "confirm the port the service binds; the recipe passes PORT"
	for _, test := range []struct {
		name      string
		files     map[string]string
		framework string
		port      int
		loopback  string
		certain   bool
		recipeFix string
		asks      bool
		start     string
	}{
		{
			name:      "axum on loopback",
			files:     map[string]string{"Cargo.toml": "[package]\nname = \"svc\"\n[dependencies]\naxum = \"0.8\"\n", "src/main.rs": "let listener = tokio::net::TcpListener::bind(\"127.0.0.1:3001\").await.unwrap();\n"},
			framework: "axum", port: 3001, loopback: "127.0.0.1", certain: true,
		},
		{
			name:      "actix reading PORT",
			files:     map[string]string{"Cargo.toml": "[package]\nname = \"svc\"\n[dependencies]\nactix-web = \"4\"\n", "src/main.rs": "let port: u16 = std::env::var(\"PORT\").ok().and_then(|p| p.parse().ok()).unwrap_or(8081);\nHttpServer::new(|| App::new()).bind((\"0.0.0.0\", port))?.run().await\n"},
			framework: "actix-web", port: 8081,
		},
		{
			// String::from and a client's new() carry some other service's
			// address; only the bind is the server's.
			name: "a client address beside a bind on PORT",
			files: map[string]string{"Cargo.toml": "[package]\nname = \"svc\"\n[dependencies]\naxum = \"0.8\"\n", "src/main.rs": "let url = String::from(\"localhost:6379\");\nlet client = Client::new(\"127.0.0.1:5432\");\n" +
				"let port = std::env::var(\"PORT\").unwrap_or_else(|_| \"3002\".to_string());\nlet listener = TcpListener::bind(format!(\"0.0.0.0:{}\", port)).await?;\n"},
			framework: "axum", port: 3002,
		},
		{
			name:      "hyper's parsed address",
			files:     map[string]string{"Cargo.toml": "[package]\nname = \"svc\"\n[dependencies]\nwarp = \"0.3\"\n", "src/main.rs": "Server::bind(&\"127.0.0.1:3003\".parse().unwrap()).serve(app).await?;\n"},
			framework: "warp", port: 3003, loopback: "127.0.0.1", certain: true,
		},
		{
			name:      "nothing readable still asks",
			files:     map[string]string{"Cargo.toml": "[package]\nname = \"svc\"\n[dependencies]\naxum = \"0.8\"\n"},
			framework: "axum", port: 3000, asks: true,
		},
		{
			name:      "rocket is moved by the recipe",
			files:     map[string]string{"Cargo.toml": "[package]\nname = \"svc\"\n[dependencies]\nrocket = \"0.5\"\n", "Rocket.toml": "[default]\nport = 8001\n"},
			framework: "rocket", port: 8001, loopback: "127.0.0.1", recipeFix: "ROCKET_ADDRESS=0.0.0.0",
		},
		{
			name:      "loco serves through its CLI",
			files:     map[string]string{"Cargo.toml": "[package]\nname = \"app\"\n[[bin]]\nname = \"app-cli\"\npath = \"src/bin/main.rs\"\n[dependencies]\nloco-rs = \"0.15\"\naxum = \"0.8\"\n"},
			framework: "loco", port: 5150, start: "/app start --binding 0.0.0.0 --port ${PORT:-5150}",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			candidate := candidateBy(t, detectCandidates(t, test.files), BuildRecipe, "rust")
			listen := listenOf(candidate)
			if candidate.Framework != test.framework || candidate.Port != test.port || listen.Loopback != test.loopback ||
				listen.LoopbackCertain != test.certain || listen.LoopbackRecipeFix != test.recipeFix ||
				slices.Contains(candidate.NeedsDecision, confirm) != test.asks || (test.start != "" && candidate.StartCommand != test.start) {
				t.Fatalf("candidate %+v, listen %+v", candidate, listen)
			}
		})
	}
}

func TestJVMPortsComeFromConfigurationAndCode(t *testing.T) {
	t.Parallel()
	spring := "<project><parent><artifactId>spring-boot-starter-parent</artifactId></parent></project>"
	for _, test := range []struct {
		name      string
		files     map[string]string
		port      int
		fixed     int
		readsPort bool
		loopback  string
	}{
		{"spring server.port is bridged", map[string]string{"pom.xml": spring, "src/main/resources/application.yml": "server:\n  port: 9000\n"}, 9000, 0, true, ""},
		{"spring default is bridged", map[string]string{"pom.xml": spring}, 8080, 0, true, ""},
		{"quarkus properties", map[string]string{"pom.xml": "<project><artifactId>quarkus-bom</artifactId></project>", "src/main/resources/application.properties": "quarkus.http.port=8081\n"}, 8081, 0, true, ""},
		{"spring bound to loopback", map[string]string{"pom.xml": spring, "src/main/resources/application.properties": "server.address=127.0.0.1\n"}, 8080, 0, true, "127.0.0.1"},
		{"vert.x listens in code", map[string]string{"pom.xml": "<project><groupId>io.vertx</groupId></project>", "src/main/java/app/MainVerticle.java": "vertx.createHttpServer().listen(8888);\n"}, 8888, 8888, false, ""},
		{"ktor follows PORT from application.conf", map[string]string{"build.gradle.kts": "dependencies { implementation(\"io.ktor:ktor-server-netty\") }", "src/main/resources/application.conf": "ktor {\n  deployment {\n    port = 8080\n    port = ${?PORT}\n  }\n}\n"}, 8080, 0, true, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			candidate := candidateBy(t, detectCandidates(t, test.files), BuildRecipe, "java")
			listen := listenOf(candidate)
			if candidate.Port != test.port || listen.Port != test.fixed || listen.ReadsPort != test.readsPort || listen.Loopback != test.loopback {
				t.Fatalf("port %d, listen %+v, evidence %+v", candidate.Port, listen, candidate.Evidence)
			}
		})
	}
}

func TestDotnetKestrelSettingsAreBridgedOrNamed(t *testing.T) {
	t.Parallel()
	project := `<Project Sdk="Microsoft.NET.Sdk.Web"><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup></Project>`
	for _, test := range []struct {
		name      string
		files     map[string]string
		port      int
		fixed     int
		loopback  string
		certain   bool
		recipeFix string
		unbridged bool
	}{
		{"no settings follow PORT", map[string]string{"api.csproj": project}, 8080, 0, "", false, "", false},
		{"one localhost endpoint is moved", map[string]string{"api.csproj": project, "appsettings.json": `{"Kestrel":{"Endpoints":{"Http":{"Url":"http://localhost:5000"}}}}`}, 5000, 0, "localhost", false, "Kestrel__Endpoints__Http__Url=http://+:${PORT:-8080}", false},
		{"several endpoints are named", map[string]string{"api.csproj": project, "appsettings.json": `{"Kestrel":{"Endpoints":{"Http":{"Url":"http://0.0.0.0:5000"},"Https":{"Url":"https://0.0.0.0:5001"}}}}`}, 8080, 0, "", false, "", true},
		{"a URL in Program.cs wins", map[string]string{"api.csproj": project, "appsettings.json": `{"Urls":"http://*:5005"}`, "Program.cs": "app.Run(\"http://localhost:5000\");\n"}, 5000, 5000, "localhost", true, "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			candidate := candidateBy(t, detectCandidates(t, test.files), BuildRecipe, "dotnet")
			listen := listenOf(candidate)
			if candidate.Port != test.port || listen.Port != test.fixed || listen.Loopback != test.loopback || listen.LoopbackCertain != test.certain ||
				listen.LoopbackRecipeFix != test.recipeFix || (listen.Unbridged != "") != test.unbridged {
				t.Fatalf("port %d, listen %+v", candidate.Port, listen)
			}
		})
	}
}

func TestDenoPortAndEntryDetection(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		files     map[string]string
		port      int
		start     string
		evidence  string
		readsPort bool
	}{
		{"Deno.serve literal", map[string]string{"deno.json": `{"tasks":{"start":"deno run -A main.ts"}}`, "main.ts": "Deno.serve({ port: 3000 }, (_req) => new Response('hi'))\n"}, 3000, "deno task start", "listens on 3000", false},
		{"oak listen", map[string]string{"deno.json": `{}`, "app.ts": "await app.listen({ port: 8080 })\n"}, 8080, "deno run --allow-all app.ts", "listens on 8080", false},
		{"deno serve flag", map[string]string{"deno.json": `{"tasks":{"start":"deno serve --port 4000 main.ts"}}`, "main.ts": "export default { fetch() { return new Response('hi') } }\n"}, 4000, "deno task start", "listens on 4000", false},
		{"deno serve default", map[string]string{"deno.json": `{"tasks":{"start":"deno serve main.ts"}}`, "main.ts": "export default { fetch() { return new Response('hi') } }\n"}, 8000, "deno task start", "deno serve listens on 8000", false},
		{"reads PORT", map[string]string{"deno.json": `{}`, "index.ts": "Deno.serve({ port: Number(Deno.env.get(\"PORT\") ?? 8000) }, handler)\n"}, 8000, "deno run --allow-all index.ts", "PORT", true},
		{"src/index.ts entry", map[string]string{"deno.json": `{}`, "src/index.ts": "Deno.serve(handler)\n"}, 8000, "deno run --allow-all src/index.ts", "8000", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			candidate := candidateBy(t, detectCandidates(t, test.files), BuildRecipe, "deno")
			reasons := ""
			for _, item := range candidate.Evidence {
				reasons += item.Reason + "\n"
			}
			if candidate.Port != test.port || candidate.StartCommand != test.start || !strings.Contains(reasons, test.evidence) ||
				len(candidate.NeedsDecision) != 0 || listenOf(candidate).ReadsPort != test.readsPort {
				t.Fatalf("candidate %+v, listen %+v", candidate, listenOf(candidate))
			}
		})
	}
}

func TestDockerfilePortFallsBackToItsEnvironmentAndSource(t *testing.T) {
	t.Parallel()
	candidates := detectCandidates(t, map[string]string{"Dockerfile": "FROM node:22\nENV PORT=4000\nCMD [\"node\", \"server.js\"]\n"})
	if candidate := candidateBy(t, candidates, BuildDockerfile, ""); candidate.Port != 4000 {
		t.Fatalf("ENV PORT: %+v", candidate)
	}
	candidates = detectCandidates(t, map[string]string{
		"Dockerfile":         "FROM elixir:1.18\nCMD [\"/app/bin/server\"]\n",
		"config/runtime.exs": "port = String.to_integer(System.get_env(\"PORT\") || \"4000\")\n",
	})
	if candidate := candidateBy(t, candidates, BuildDockerfile, ""); candidate.Port != 4000 {
		t.Fatalf("Phoenix runtime.exs: %+v", candidate)
	}
	candidates = detectCandidates(t, map[string]string{
		"Dockerfile":        "FROM node:22\nCMD [\"node\", \"server.js\"]\n",
		"package.json":      `{"scripts":{"start":"node server.js"},"dependencies":{"express":"4"}}`,
		"package-lock.json": "{}",
		"server.js":         "const host = process.env.HOST || 'localhost'\napp.listen(3100, host)\n",
	})
	dockerfile := candidateBy(t, candidates, BuildDockerfile, "")
	listen := listenOf(dockerfile)
	if dockerfile.Port != 3100 || listen.LoopbackVariable != "HOST" || len(dockerfile.NetworkVariables) != 1 ||
		dockerfile.NetworkVariables[0].Name != "HOST" || dockerfile.NetworkVariables[0].Value != "0.0.0.0" {
		t.Fatalf("Dockerfile sibling of a Node server: %+v, listen %+v", dockerfile, listen)
	}
	recipe := candidateBy(t, candidates, BuildRecipe, "node")
	if listenOf(recipe).LoopbackRecipeFix != "HOST=0.0.0.0" || len(recipe.NetworkVariables) != 0 {
		t.Fatalf("the recipe sets HOST itself: %+v", recipe)
	}
	// The recipe's code facts describe the code; the Dockerfile runs its own
	// CMD, and one fronting that code on another port is not blocked by it.
	server := map[string]string{
		"package.json":      `{"scripts":{"start":"node server.js"},"dependencies":{"express":"4"}}`,
		"package-lock.json": "{}",
		"server.js":         "app.listen(3000, '127.0.0.1')\n",
	}
	for _, test := range []struct {
		expose   string
		loopback string
	}{{"80", ""}, {"3000", "127.0.0.1"}} {
		files := map[string]string{"Dockerfile": "FROM node:22\nEXPOSE " + test.expose + "\nCMD [\"/start.sh\"]\n"}
		for name, content := range server {
			files[name] = content
		}
		candidates := detectCandidates(t, files)
		dockerfile := listenOf(candidateBy(t, candidates, BuildDockerfile, ""))
		if dockerfile.Loopback != test.loopback || dockerfile.LoopbackCertain {
			t.Fatalf("EXPOSE %s: Dockerfile listen %+v", test.expose, dockerfile)
		}
		if recipe := listenOf(candidateBy(t, candidates, BuildRecipe, "node")); !recipe.LoopbackCertain {
			t.Fatalf("EXPOSE %s: recipe listen %+v", test.expose, recipe)
		}
	}
}

func TestSignInPackagesAreNamedBesideTheTrustTheyNeed(t *testing.T) {
	t.Parallel()
	candidates := detectCandidates(t, map[string]string{
		"api.csproj": `<Project Sdk="Microsoft.NET.Sdk.Web"><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup>` +
			`<ItemGroup><PackageReference Include="Microsoft.AspNetCore.Authentication.OpenIdConnect" Version="8.0.0" /></ItemGroup></Project>`,
	})
	dotnet := candidateBy(t, candidates, BuildRecipe, "dotnet")
	spring := candidateBy(t, detectCandidates(t, map[string]string{
		"pom.xml": "<project><parent><artifactId>spring-boot-starter-parent</artifactId></parent><dependencies><dependency><artifactId>spring-boot-starter-oauth2-client</artifactId></dependency></dependencies></project>",
	}), BuildRecipe, "java")
	for _, candidate := range []DetectedCandidate{dotnet, spring} {
		found := false
		for _, item := range candidate.Evidence {
			found = found || strings.Contains(item.Reason, "redirect URIs from the forwarded scheme")
		}
		if !found {
			t.Fatalf("no sign-in evidence: %+v", candidate.Evidence)
		}
	}
}

func TestDetectedPythonCommandsFollowPORT(t *testing.T) {
	t.Parallel()
	for name, files := range map[string]map[string]string{
		"django":    {"requirements.txt": "django\n", "manage.py": "", "mysite/wsgi.py": ""},
		"flask":     {"requirements.txt": "flask\n", "app.py": "app = Flask(__name__)\n"},
		"streamlit": {"requirements.txt": "streamlit\n", "streamlit_app.py": "import streamlit as st\n"},
	} {
		candidate := candidateBy(t, detectCandidates(t, files), BuildRecipe, "python")
		if !strings.Contains(candidate.StartCommand, "${PORT:-") || !listenOf(candidate).ReadsPort || listenOf(candidate).Port != 0 {
			t.Fatalf("%s: start %q, listen %+v", name, candidate.StartCommand, listenOf(candidate))
		}
	}
}

func TestAHostReadWithALoopbackFallbackGetsAHostVariable(t *testing.T) {
	t.Parallel()
	candidate := candidateBy(t, detectCandidates(t, map[string]string{
		"Cargo.toml":  "[package]\nname = \"svc\"\n[dependencies]\naxum = \"0.8\"\n",
		"src/main.rs": "let host = std::env::var(\"HOST\").unwrap_or(\"127.0.0.1\".into());\nlet port = std::env::var(\"PORT\").unwrap_or(\"3000\".into());\nlet listener = TcpListener::bind(format!(\"{host}:{port}\")).await?;\n",
	}), BuildRecipe, "rust")
	listen := listenOf(candidate)
	if listen.Loopback != "127.0.0.1" || listen.LoopbackVariable != "HOST" || listen.LoopbackCertain ||
		len(candidate.NetworkVariables) != 1 || candidate.NetworkVariables[0].Name != "HOST" || candidate.NetworkVariables[0].Value != "0.0.0.0" {
		t.Fatalf("candidate %+v, listen %+v", candidate, listen)
	}
}
