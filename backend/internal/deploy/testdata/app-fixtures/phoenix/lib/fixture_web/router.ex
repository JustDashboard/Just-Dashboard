defmodule FixtureWeb.Router do
  use Phoenix.Router

  get "/", FixtureWeb.PageController, :index
end
