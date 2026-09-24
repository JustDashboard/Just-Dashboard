package deploy

import (
	"strings"
	"testing"
)

// The listen scanners read one file each; what they must get right is the
// difference between a port the code fixes, a port it takes from PORT, and
// a host nothing outside the container reaches.

func TestScriptListenReadsPortsHostsAndFallbacks(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, source             string
		ports, fallbacks         []int
		readsPort                bool
		loopback, open, defaults int
		hostFallback             bool
	}{
		{name: "express literal", source: "const app = express()\napp.listen(4000, () => console.log('up'))\n", ports: []int{4000}, open: 1},
		{name: "PORT with a fallback", source: "const port = process.env.PORT || 5000\napp.listen(port)\n", fallbacks: []int{5000}, readsPort: true, open: 1},
		{name: "Number around PORT", source: "app.listen(Number(process.env.PORT ?? \"8080\"))\n", fallbacks: []int{8080}, readsPort: true, open: 1},
		{name: "destructured PORT", source: "const { PORT = 3000 } = process.env\napp.listen(PORT)\n", readsPort: true, open: 1},
		{name: "constant port", source: "const PORT = 3001;\nserver.listen(PORT);\n", ports: []int{3001}, open: 1},
		{name: "explicit loopback", source: "server.listen(8080, '127.0.0.1')\n", ports: []int{8080}, loopback: 1},
		{name: "loopback constant", source: "const HOST = 'localhost'\napp.listen(3000, HOST, () => {})\n", ports: []int{3000}, loopback: 1},
		{name: "HOST read with a loopback fallback", source: "const host = process.env.HOST || 'localhost'\napp.listen(3000, host)\n", ports: []int{3000}, hostFallback: true},
		{name: "fastify without host", source: "import Fastify from 'fastify'\nawait app.listen({ port: Number(process.env.PORT) || 3000 })\n", fallbacks: []int{3000}, readsPort: true, defaults: 1},
		{name: "fastify with host", source: "const fastify = require('fastify')()\nfastify.listen({ port: 3000, host: '0.0.0.0' })\n", ports: []int{3000}, open: 1},
		{name: "nest fastify adapter", source: "new FastifyAdapter()\nawait app.listen(3000)\n", ports: []int{3000}, defaults: 1},
		{name: "Bun.serve port", source: "Bun.serve({ port: 8080, fetch(req) { return new Response('ok') } })\n", ports: []int{8080}, open: 1},
		{name: "Bun.serve without port follows PORT", source: "Bun.serve({ fetch(req) { return new Response('ok') } })\n", fallbacks: []int{3000}, readsPort: true, open: 1},
		{name: "Deno.serve handler", source: "Deno.serve((req) => new Response('hi'))\n", ports: []int{8000}, open: 1},
		{name: "Deno.serve options", source: "Deno.serve({ port: 3000, hostname: '127.0.0.1' }, handler)\n", ports: []int{3000}, loopback: 1},
		{name: "Bun default export", source: "export default { port: 4000, fetch: app.fetch }\n", ports: []int{4000}, open: 1},
		{name: "commented example", source: "// app.listen(4000, '127.0.0.1')\napp.listen(process.env.PORT)\n", readsPort: true, open: 1},
		{name: "elysia", source: "new Elysia().get('/', () => 'hi').listen(8080)\n", ports: []int{8080}, open: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			report := scanScriptListen("server.js", []byte(test.source))
			if got := portsOf(report.ports); !equalInts(got, test.ports) {
				t.Fatalf("ports = %v, want %v (%+v)", got, test.ports, report)
			}
			if got := portsOf(report.fallbacks); !equalInts(got, test.fallbacks) {
				t.Fatalf("fallbacks = %v, want %v (%+v)", got, test.fallbacks, report)
			}
			if (report.readsPort != nil) != test.readsPort || len(report.loopback) != test.loopback ||
				len(report.open) != test.open || len(report.defaults) != test.defaults || (report.hostFallback != nil) != test.hostFallback {
				t.Fatalf("report = %+v", report)
			}
		})
	}
	report := scanScriptListen("src/server.ts", []byte("import express from 'express'\n\nconst app = express()\napp.listen(4000, '127.0.0.1')\n"))
	if len(report.loopback) != 1 || report.loopback[0].at() != "src/server.ts:4" || report.loopback[0].host != "127.0.0.1" {
		t.Fatalf("loopback mark = %+v", report.loopback)
	}
}

func TestCompiledAndPythonListenScanners(t *testing.T) {
	t.Parallel()
	goReport := scanGoListen("main.go", []byte("package main\n\nfunc main() {\n\tr := gin.Default()\n\tr.Run(\":8080\")\n}\n"), false)
	if got := portsOf(goReport.ports); !equalInts(got, []int{8080}) || len(goReport.open) != 1 {
		t.Fatalf("gin literal = %+v", goReport)
	}
	goReport = scanGoListen("main.go", []byte("package main\n\nfunc main() {\n\tport := os.Getenv(\"PORT\")\n\tif port == \"\" {\n\t\tport = \"9090\"\n\t}\n\thttp.ListenAndServe(\":\"+port, nil)\n}\n"), false)
	if goReport.readsPort == nil || !equalInts(portsOf(goReport.fallbacks), []int{9090}) || len(goReport.open) != 1 {
		t.Fatalf("PORT with fallback = %+v", goReport)
	}
	goReport = scanGoListen("cmd/api/main.go", []byte("package main\n\nconst addr = \"localhost:8080\"\n\nfunc main() { log.Fatal(http.ListenAndServe(addr, nil)) }\n"), false)
	if len(goReport.loopback) != 1 || goReport.loopback[0].port != 8080 {
		t.Fatalf("loopback through a constant = %+v", goReport)
	}
	goReport = scanGoListen("main.go", []byte("package main\nfunc main() {\n\tln, _ := net.Listen(\"tcp\", \"127.0.0.1:50051\")\n\ts := &http.Server{Addr: \":8081\"}\n}\n"), false)
	if len(goReport.loopback) != 1 || len(goReport.open) != 1 || !equalInts(portsOf(goReport.ports), []int{50051, 8081}) {
		t.Fatalf("net.Listen and Addr = %+v", goReport)
	}

	for _, test := range []struct {
		name, source string
		port         int
		host         string
		readsPort    bool
		fallback     int
	}{
		{"axum bind literal", `let listener = tokio::net::TcpListener::bind("127.0.0.1:3000").await.unwrap();`, 3000, "127.0.0.1", false, 0},
		{"actix tuple", `HttpServer::new(|| App::new()).bind(("127.0.0.1", 8080))?.run().await`, 8080, "127.0.0.1", false, 0},
		{"socket addr array", `let addr = SocketAddr::from(([0, 0, 0, 0], 3000));`, 3000, "0.0.0.0", false, 0},
		{"warp run", `warp::serve(routes).run(([127, 0, 0, 1], 3030)).await;`, 3030, "127.0.0.1", false, 0},
		{"PORT with fallback", "let port = std::env::var(\"PORT\").unwrap_or_else(|_| \"3000\".to_string());\nlet listener = TcpListener::bind(format!(\"0.0.0.0:{}\", port)).await?;", 0, "0.0.0.0", true, 3000},
	} {
		report := scanRustListen("src/main.rs", []byte(test.source))
		if (report.readsPort != nil) != test.readsPort || (test.fallback > 0 && !equalInts(portsOf(report.fallbacks), []int{test.fallback})) {
			t.Fatalf("%s: %+v", test.name, report)
		}
		marks := append(append([]sourceMark{}, report.loopback...), report.open...)
		if len(marks) != 1 || marks[0].host != test.host || marks[0].port != test.port {
			t.Fatalf("%s: marks = %+v", test.name, marks)
		}
	}

	python := scanPythonListen("app.py", []byte("from flask import Flask\napp = Flask(__name__)\n\nif __name__ == '__main__':\n    app.run(port=5000)\n"))
	if len(python.defaults) != 1 || python.defaults[0].host != "127.0.0.1" || !equalInts(portsOf(python.ports), []int{5000}) {
		t.Fatalf("flask app.run = %+v", python)
	}
	python = scanPythonListen("main.py", []byte("import os, uvicorn\nif __name__ == '__main__':\n    uvicorn.run(app, host='0.0.0.0', port=int(os.environ.get('PORT', 8080)))\n"))
	if len(python.open) != 1 || python.readsPort == nil || !equalInts(portsOf(python.fallbacks), []int{8080}) {
		t.Fatalf("uvicorn.run = %+v", python)
	}
	python = scanPythonListen("worker.py", []byte("import asyncio\nasyncio.run(main())\n"))
	if !python.empty() {
		t.Fatalf("asyncio.run read as a listener: %+v", python)
	}
	gunicorn := scanGunicornConfig("gunicorn.conf.py", []byte("workers = 4\nbind = \"0.0.0.0:5000\"\n"))
	if !equalInts(portsOf(gunicorn.ports), []int{5000}) || len(gunicorn.open) != 1 {
		t.Fatalf("gunicorn bind = %+v", gunicorn)
	}
	gunicorn = scanGunicornConfig("gunicorn.conf.py", []byte("import os\nbind = f\"0.0.0.0:{os.environ.get('PORT', '8000')}\"\n"))
	if gunicorn.readsPort == nil || len(gunicorn.ports) != 0 {
		t.Fatalf("gunicorn bind from PORT = %+v", gunicorn)
	}
}

func TestJVMAndDotnetConfigurationScanners(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		file, content string
		config        int
		readsPort     bool
		fallback      int
		loopback      int
	}{
		{"application.properties", "spring.application.name=demo\nserver.port=9000\n", 9000, false, 0, 0},
		{"application.properties", "server.port=${PORT:8081}\n", 0, true, 8081, 0},
		{"application.properties", "quarkus.http.port=8081\nquarkus.http.host=127.0.0.1\n", 8081, false, 0, 1},
		{"application.yml", "spring:\n  application:\n    name: demo\nserver:\n  port: 9000 # the API\n  address: 127.0.0.1\n", 9000, false, 0, 1},
		{"application.yml", "micronaut:\n  server:\n    port: ${PORT:8082}\n", 0, true, 8082, 0},
		{"application.yaml", "ktor:\n  deployment:\n    port: \"$PORT:8080\"\n", 0, true, 8080, 0},
		{"application.conf", "ktor {\n  deployment {\n    port = 8080\n    port = ${?PORT}\n  }\n}\n", 0, true, 8080, 0},
		{"application.conf", "ktor {\n  deployment {\n    port = 8081\n  }\n}\n", 8081, false, 0, 0},
	} {
		report := scanJVMConfig("src/main/resources/"+test.file, []byte(test.content))
		if got := portsOf(report.configPorts); (test.config == 0 && len(got) != 0) || (test.config > 0 && !equalInts(got, []int{test.config})) ||
			(report.readsPort != nil) != test.readsPort || (test.fallback > 0 && !equalInts(portsOf(report.fallbacks), []int{test.fallback})) ||
			len(report.loopback) != test.loopback {
			t.Fatalf("%s %q: %+v", test.file, test.content, report)
		}
	}
	vertx := scanJVMSource("src/main/java/com/example/MainVerticle.java", []byte("vertx.createHttpServer().requestHandler(req -> {}).listen(8888, http -> {});\n"))
	if !equalInts(portsOf(vertx.ports), []int{8888}) || len(vertx.open) != 1 {
		t.Fatalf("vert.x = %+v", vertx)
	}
	ktor := scanJVMSource("src/main/kotlin/Application.kt", []byte("fun main() {\n    embeddedServer(Netty, port = 8081, host = \"127.0.0.1\", module = Application::module).start(wait = true)\n}\n"))
	if !equalInts(portsOf(ktor.ports), []int{8081}) || len(ktor.loopback) != 1 {
		t.Fatalf("ktor = %+v", ktor)
	}
	javalin := scanJVMSource("src/main/java/App.java", []byte("Javalin.create().get(\"/\", ctx -> ctx.result(\"hi\")).start(7070);\n"))
	if !equalInts(portsOf(javalin.ports), []int{7070}) {
		t.Fatalf("javalin = %+v", javalin)
	}

	settings := parseKestrelSettings([]byte("{\n  // comments are allowed\n  \"Kestrel\": { \"endpoints\": { \"Http\": { \"url\": \"http://localhost:5000\" } } },\n  \"Urls\": \"http://*:5001\",\n}\n"))
	if settings.endpoints["Http"] != "http://localhost:5000" || settings.urls != "http://*:5001" {
		t.Fatalf("kestrel settings = %+v", settings)
	}
	program := scanDotnetSource("Program.cs", []byte("var app = builder.Build();\napp.MapGet(\"/\", () => \"hi\");\napp.Run(\"http://localhost:5000\");\n"))
	if len(program.loopback) != 1 || program.loopback[0].port != 5000 {
		t.Fatalf("Program.cs = %+v", program)
	}
	program = scanDotnetSource("Program.cs", []byte("builder.WebHost.ConfigureKestrel(o => o.ListenAnyIP(8080));\n"))
	if len(program.open) != 1 || program.open[0].port != 8080 {
		t.Fatalf("ListenAnyIP = %+v", program)
	}

	if port := dockerfileEnvPort([]byte("FROM node AS build\nENV PORT=3000\nFROM node\nENV PORT 4000\nCMD node server.js\n")); port != 4000 {
		t.Fatalf("Dockerfile ENV PORT = %d", port)
	}
	if port := dockerfileEnvPort([]byte("FROM node\nENV PORT=3000\nFROM nginx\n")); port != 0 {
		t.Fatalf("an earlier stage's PORT counted: %d", port)
	}
}

func TestCommandListenReadsFlagsPrefixesAndDefaults(t *testing.T) {
	t.Parallel()
	scripts := map[string]string{
		"start":   "next start -p 3001",
		"serve":   "PORT=4000 node server.js",
		"preview": "vite preview",
		"local":   "next start -H localhost",
		"prod":    "npm run serve",
	}
	for _, test := range []struct {
		command            string
		tool               string
		port, fallback     int
		followsPort        bool
		host, defaultHost  string
		hostEnv, devServer string
	}{
		{command: "npm run start", tool: "next start", port: 3001},
		{command: "npx prisma migrate deploy && npm run serve", tool: "node", port: 4000},
		{command: "pnpm run prod", tool: "node", port: 4000},
		{command: "bun run preview", tool: "vite preview", defaultHost: "localhost"},
		{command: "yarn run local", tool: "next start", host: "localhost"},
		{command: "npm start", tool: "next start", port: 3001},
		{command: "cross-env NODE_ENV=production PORT=5000 node dist/main.js", tool: "node", port: 5000},
		{command: "uvicorn main:app --host 0.0.0.0 --port ${PORT:-8000}", tool: "uvicorn", followsPort: true, fallback: 8000, host: "0.0.0.0", defaultHost: "127.0.0.1", hostEnv: "UVICORN_HOST"},
		{command: "uvicorn main:app --port 5000", tool: "uvicorn", port: 5000, defaultHost: "127.0.0.1", hostEnv: "UVICORN_HOST"},
		{command: "uvicorn main:app --reload", tool: "uvicorn", defaultHost: "127.0.0.1", hostEnv: "UVICORN_HOST", devServer: "uvicorn --reload"},
		{command: "gunicorn -b 0.0.0.0:$PORT app:app", tool: "gunicorn", followsPort: true, host: "0.0.0.0"},
		{command: "python manage.py migrate --noinput && gunicorn mysite.wsgi:application --bind 0.0.0.0:${PORT:-8000}", tool: "gunicorn", followsPort: true, fallback: 8000, host: "0.0.0.0"},
		{command: "gunicorn --bind 127.0.0.1:8000 app:app", tool: "gunicorn", port: 8000, host: "127.0.0.1"},
		{command: "gunicorn -c config/gunicorn.py app:app", tool: "gunicorn"},
		{command: "python manage.py migrate && python manage.py runserver", tool: "manage.py runserver", defaultHost: "127.0.0.1", devServer: "manage.py runserver"},
		{command: "python manage.py runserver 0.0.0.0:8000", tool: "manage.py runserver", port: 8000, host: "0.0.0.0", defaultHost: "127.0.0.1", devServer: "manage.py runserver"},
		{command: "flask run", tool: "flask run", defaultHost: "127.0.0.1", hostEnv: "FLASK_RUN_HOST", devServer: "flask run"},
		{command: "fastapi dev main.py", tool: "fastapi dev", defaultHost: "127.0.0.1", devServer: "fastapi dev"},
		{command: "streamlit run app.py --server.port=8501", tool: "streamlit", port: 8501},
		{command: "python -m hypercorn main:app", tool: "hypercorn", defaultHost: "127.0.0.1"},
		{command: "uv run uvicorn main:app --host 127.0.0.1", tool: "uvicorn", host: "127.0.0.1", defaultHost: "127.0.0.1", hostEnv: "UVICORN_HOST"},
		{command: "java -Dserver.port=$PORT -jar target/app.jar", tool: "java", followsPort: true},
		{command: "dotnet app.dll --urls http://localhost:5000", tool: "dotnet", port: 5000, host: "localhost"},
		{command: "vite preview --host --port 4173", tool: "vite preview", port: 4173, host: "0.0.0.0", defaultHost: "localhost"},
		{command: "serve -s build -l 3000", tool: "serve", port: 3000},
		{command: "tsc -p tsconfig.json && node dist/index.js", tool: "node"},
		{command: "deno serve --port 3000 main.ts", tool: "deno", port: 3000},
	} {
		facts := parseCommandListen(test.command, scripts)
		if facts.tool != test.tool || facts.port != test.port || facts.fallback != test.fallback || facts.followsPort != test.followsPort ||
			facts.host != test.host || facts.defaultHost != test.defaultHost || facts.hostEnv != test.hostEnv || facts.devServer != test.devServer {
			t.Fatalf("%q = %+v", test.command, facts)
		}
	}
	if facts := parseCommandListen("gunicorn -c config/gunicorn.py app:app", nil); facts.configFile != "config/gunicorn.py" {
		t.Fatalf("gunicorn config = %+v", facts)
	}
	uvicorn := parseCommandListen("uvicorn main:app", nil)
	if host, moved := uvicorn.boundHost(map[string]bool{"UVICORN_HOST": true}); host != "" || moved != "UVICORN_HOST" {
		t.Fatalf("recipe environment did not move uvicorn: %q %q", host, moved)
	}
	if host, moved := uvicorn.boundHost(nil); host != "127.0.0.1" || moved != "" {
		t.Fatalf("uvicorn without the recipe: %q %q", host, moved)
	}
}

func TestSimpleConfigReadersAndStoredListenFacts(t *testing.T) {
	t.Parallel()
	yaml := flattenSimpleYAML([]byte("server:\n  port: 9000\n  servlet:\n    context-path: /api\nspring:\n  profiles:\n    - prod\n---\nserver:\n  port: 9100\n"))
	if yaml["server.port"].text != "9000" || yaml["server.port"].line != 2 || yaml["server.servlet.context-path"].text != "/api" {
		t.Fatalf("yaml = %+v", yaml)
	}
	properties := flattenProperties([]byte("# comment\nserver.port = 9000\nquarkus.http.port: 8081\nmicronaut.server.port 8082\n"))
	if properties["server.port"].text != "9000" || properties["quarkus.http.port"].text != "8081" || properties["micronaut.server.port"].text != "8082" {
		t.Fatalf("properties = %+v", properties)
	}
	if text := listenText("listen(3000, 'postgres://user:hunter2@db:5432/app')"); !strings.Contains(text, "withheld") {
		t.Fatalf("credential-shaped fact kept: %q", text)
	}
	if text := listenText(strings.Repeat("x", 300)); len(text) != 200 {
		t.Fatalf("fact not bounded: %d", len(text))
	}
	valid := DetectedCandidate{
		Listen:           &DetectedListen{Port: 4000, PortFrom: "server.js:3 listen(4000)", Loopback: "localhost", LoopbackVariable: "HOST"},
		NetworkVariables: []DetectedNetworkVariable{{Name: "AUTH_TRUST_HOST", Value: "true", Reason: "next-auth 5"}, {Name: "NEXTAUTH_URL", DomainTemplate: "{{scheme}}://{{hostname}}", Reason: "next-auth 4"}},
	}
	if err := validateNetworkFacts(valid); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []DetectedCandidate{
		{Listen: &DetectedListen{Port: 70000}},
		{Listen: &DetectedListen{PortFrom: "a\nb"}},
		{Listen: &DetectedListen{LoopbackVariable: "not a name"}},
		{NetworkVariables: []DetectedNetworkVariable{{Name: "X", Reason: "neither a value nor a template"}}},
		{NetworkVariables: []DetectedNetworkVariable{{Name: "X", DomainTemplate: "https://example.com", Reason: "no hostname"}}},
		{NetworkVariables: []DetectedNetworkVariable{{Name: "X", Value: "postgres://u:secret@h/db", Reason: "credential"}}},
	} {
		if validateNetworkFacts(invalid) == nil {
			t.Fatalf("accepted %+v", invalid)
		}
	}
}

func portsOf(marks []sourceMark) []int {
	ports := make([]int, 0, len(marks))
	for _, mark := range marks {
		ports = append(ports, mark.port)
	}
	return ports
}

func equalInts(got, want []int) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}

func TestShorthandListenOptionsFollowTheirVariables(t *testing.T) {
	t.Parallel()
	// The live Deno fixture: the port is a PORT read with a fallback, passed
	// by shorthand, so it is neither a literal nor Deno.serve's default.
	report := scanScriptListen("main.ts", []byte("const port = Number(Deno.env.get(\"PORT\") ?? \"8000\")\nDeno.serve({ port, hostname: \"0.0.0.0\" }, () =>\n  new Response(\"hi\"),\n)\n"))
	if len(report.ports) != 0 || report.readsPort == nil || !equalInts(portsOf(report.fallbacks), []int{8000}) || len(report.open) != 1 {
		t.Fatalf("shorthand port = %+v", report)
	}
	report = scanScriptListen("app.js", []byte("const port = 4000\nconst host = '127.0.0.1'\nawait fastify.listen({ port, host })\n"))
	if !equalInts(portsOf(report.ports), []int{4000}) || len(report.loopback) != 1 {
		t.Fatalf("shorthand host = %+v", report)
	}
}

func TestHostReadsCountAsLoopbackOnlyWithALoopbackFallback(t *testing.T) {
	t.Parallel()
	rust := scanRustListen("src/main.rs", []byte("let host = std::env::var(\"HOST\").unwrap_or(\"127.0.0.1\".into());\nlet listener = TcpListener::bind(format!(\"{host}:3000\")).await?;\n"))
	if rust.hostFallback == nil || rust.hostFallback.host != "127.0.0.1" {
		t.Fatalf("rust HOST fallback = %+v", rust)
	}
	goReport := scanGoListen("main.go", []byte("package main\nfunc main() {\n\thost := os.Getenv(\"HOST\")\n\tif host == \"\" {\n\t\thost = \"localhost\"\n\t}\n\thttp.ListenAndServe(host+\":8080\", nil)\n}\n"), false)
	if goReport.hostFallback == nil || goReport.hostFallback.host != "localhost" {
		t.Fatalf("go HOST fallback = %+v", goReport)
	}
	// Unset, `os.Getenv("HOST") + ":8080"` is every interface.
	goReport = scanGoListen("main.go", []byte("package main\nfunc main() {\n\thttp.ListenAndServe(os.Getenv(\"HOST\")+\":8080\", nil)\n}\n"), false)
	if goReport.hostFallback != nil {
		t.Fatalf("a bare HOST read counted as loopback: %+v", goReport)
	}
}
