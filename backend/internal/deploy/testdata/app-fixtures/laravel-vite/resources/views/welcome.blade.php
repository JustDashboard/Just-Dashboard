<!doctype html>
<html>
<head>
    <meta charset="utf-8">
    <title>Laravel with Vite</title>
    @vite(['resources/css/app.css', 'resources/js/app.js'])
</head>
<body>
    <h1 id="api">{{ request()->isSecure() ? 'https' : 'http' }}</h1>
</body>
</html>
