defmodule Fixture.MixProject do
  use Mix.Project

  def project do
    [
      app: :fixture,
      version: "0.1.0",
      elixir: "~> 1.15",
      start_permanent: Mix.env() == :prod,
      deps: deps()
    ]
  end

  def application do
    [mod: {Fixture.Application, []}, extra_applications: [:logger]]
  end

  defp deps do
    [
      {:phoenix, "~> 1.7"},
      {:jason, "~> 1.4"},
      {:bandit, "~> 1.5"}
    ]
  end
end
