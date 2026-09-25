defmodule FixtureWeb.PageController do
  use Phoenix.Controller, formats: [:html]

  def index(conn, _params) do
    html(conn, "<h1>https://phoenix.build-value.test</h1>")
  end
end
