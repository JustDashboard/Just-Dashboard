defmodule FixtureWeb.Endpoint do
  use Phoenix.Endpoint, otp_app: :fixture

  plug Plug.RequestId
  plug FixtureWeb.Router
end
