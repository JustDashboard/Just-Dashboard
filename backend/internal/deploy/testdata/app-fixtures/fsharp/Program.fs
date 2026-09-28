open System
open Microsoft.AspNetCore.Builder
open Microsoft.AspNetCore.Http

[<EntryPoint>]
let main args =
    let builder = WebApplication.CreateBuilder(args)
    let app = builder.Build()
    app.MapGet("/", Func<IResult>(fun () -> Results.Content("<h1>https://fsharp.build-value.test</h1>", "text/html"))) |> ignore
    app.Run()
    0
