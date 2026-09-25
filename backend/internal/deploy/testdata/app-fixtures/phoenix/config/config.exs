import Config

config :fixture, FixtureWeb.Endpoint,
  adapter: Bandit.PhoenixAdapter,
  url: [host: "localhost"],
  render_errors: [formats: [json: FixtureWeb.ErrorJSON], layout: false]

config :phoenix, :json_library, Jason

import_config "#{config_env()}.exs"
