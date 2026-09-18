var builder = WebApplication.CreateBuilder(args);
var app = builder.Build();
app.MapGet("/", () => Results.Content("<h1>https://dotnet.build-value.test</h1>", "text/html"));
app.Run();
