// Stands in for a bundler: writes the page the ASP.NET Core project serves
// from wwwroot once dotnet publish copies it there.
const fs = require("node:fs")
fs.mkdirSync("dist", { recursive: true })
fs.writeFileSync("dist/index.html", `<h1>https://dotnet-spa.build-value.test</h1><p>built by Node ${process.version}</p>\n`)
